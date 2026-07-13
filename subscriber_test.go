package gomsgq

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// the publisher wakes registered readers with SIGUSR2 (ThreadSignal); when
	// publisher and subscriber live in the same test process that signal would
	// kill us with its default action
	signal.Ignore(syscall.SIGUSR2)
	os.Exit(m.Run())
}

// setupQueue creates one shared-memory queue and returns two independent Msgq
// handles for it, mirroring the publisher and subscriber living in separate
// processes.
func setupQueue(t *testing.T, size int64) (pubQ Msgq, subQ Msgq) {
	t.Helper()
	name := fmt.Sprintf("gomsgq_test_%d_%s", os.Getpid(), t.Name())
	if err := pubQ.Init(name, size); err != nil {
		t.Fatalf("publisher queue init: %v", err)
	}
	if err := subQ.Init(name, size); err != nil {
		t.Fatalf("subscriber queue init: %v", err)
	}
	t.Cleanup(func() {
		pubQ.Close()
		subQ.Close()
		// Msgq.Init does not retain the file handle, rebuild its path
		path := pathPrefix()
		if IsPrefixedMsgq() {
			path += MSGQ_PREFIX
		}
		if OPENPILOT_PREFIX != "" {
			path += OPENPILOT_PREFIX + "/"
		}
		os.Remove(path + name)
	})
	return pubQ, subQ
}

// uniform returns a message of the given size where every byte carries the
// same marker value, so any torn read is detectable as mixed content.
func uniform(size int, marker byte) []byte {
	return bytes.Repeat([]byte{marker}, size)
}

func TestShadowReaderBasic(t *testing.T) {
	pubQ, subQ := setupQueue(t, 1<<16)
	pub := MsgqPublisher{}
	pub.Init(pubQ)
	sub := MsgqSubscriber{Shadow: true}
	sub.Init(subQ)

	for i := range 20 {
		msg := uniform(100+i, byte(i+1))
		pub.Send(msg)
		got := sub.Read()
		if !bytes.Equal(got, msg) {
			t.Fatalf("message %d: got %d bytes (marker %v), expected %d bytes of %v",
				i, len(got), got[:min(len(got), 1)], len(msg), msg[0])
		}
	}
	if got := sub.Read(); got != nil {
		t.Fatalf("expected nil when caught up, got %d bytes", len(got))
	}
}

func TestRegisteredReaderBasic(t *testing.T) {
	pubQ, subQ := setupQueue(t, 1<<16)
	pub := MsgqPublisher{}
	pub.Init(pubQ)
	sub := MsgqSubscriber{}
	sub.Init(subQ)

	for i := range 20 {
		msg := uniform(64, byte(i+1))
		pub.Send(msg)
		got := sub.Read()
		if !bytes.Equal(got, msg) {
			t.Fatalf("message %d: mismatch", i)
		}
	}
}

// TestShadowReaderLapped reproduces the crash from pfeiferj/mapd#88: the
// publisher wraps past a slow shadow reader so that the write offset is
// numerically ahead of the read offset on a newer cycle. The reader's stale
// offset then points into the middle of a newer message's payload, and the
// bytes there get interpreted as a size tag. Without lap validation that
// garbage size panics ("Invalid Msgq message size"); with it the reader must
// discard its position and recover.
func TestShadowReaderLapped(t *testing.T) {
	pubQ, subQ := setupQueue(t, 4096)
	pub := MsgqPublisher{}
	pub.Init(pubQ)
	sub := MsgqSubscriber{Shadow: true}
	sub.Init(subQ)

	// cycle 0 uses 1000-byte messages: offsets 0, 1008, 2016, 3024
	pub.Send(uniform(1000, 0x01))
	if got := sub.Read(); !bytes.Equal(got, uniform(1000, 0x01)) {
		t.Fatalf("failed to read first message")
	}
	// reader now sits at offset 1008 of cycle 0
	pub.Send(uniform(1000, 0x02))
	pub.Send(uniform(1000, 0x03))
	pub.Send(uniform(1000, 0x04))

	// cycle 1 uses 500-byte messages: offsets 0, 512, 1024, ... -- offset 1008
	// is now mid-payload, and the payload byte 0x7f repeated eight times reads
	// as size 0x7f7f7f7f7f7f7f7f
	for range 7 {
		pub.Send(uniform(500, 0x7f))
	}

	// on the unvalidated shadow path this call aborts the process
	if got := sub.Read(); got != nil {
		t.Fatalf("expected nil after being lapped, got %d bytes", len(got))
	}

	// the reader must have recovered to the write head: the next message is
	// readable
	msg := uniform(500, 0x05)
	pub.Send(msg)
	if got := sub.Read(); !bytes.Equal(got, msg) {
		t.Fatalf("reader did not recover after lap: got %v", got)
	}
}

// TestShadowReaderPublisherRestart covers the publisher re-initializing (its
// write pointer jumps back to zero) while a shadow reader still holds a
// position from the previous run.
func TestShadowReaderPublisherRestart(t *testing.T) {
	pubQ, subQ := setupQueue(t, 1<<16)
	pub := MsgqPublisher{}
	pub.Init(pubQ)
	sub := MsgqSubscriber{Shadow: true}
	sub.Init(subQ)

	for i := range 5 {
		pub.Send(uniform(1000, byte(i+1)))
		sub.Read()
	}

	pub2 := MsgqPublisher{}
	pub2.Init(pubQ)
	pub2.Send(uniform(1000, 0x11))
	// stale position from before the restart: must not panic, must not return
	// garbage; either nil (discarded) or a whole valid message is acceptable
	if got := sub.Read(); got != nil && !bytes.Equal(got, uniform(1000, 0x11)) {
		t.Fatalf("got torn/garbage data after publisher restart")
	}
	msg := uniform(1000, 0x12)
	pub2.Send(msg)
	if got := sub.Read(); !bytes.Equal(got, msg) {
		t.Fatalf("reader did not recover after publisher restart")
	}
}

// TestShadowReaderStress runs a publisher at full speed against a concurrent
// shadow reader and verifies that every message the reader returns is intact
// (single repeated marker byte) and that the reader never panics, no matter
// how often it gets lapped mid-read.
func TestShadowReaderStress(t *testing.T) {
	pubQ, subQ := setupQueue(t, 1<<20)
	pub := MsgqPublisher{}
	pub.Init(pubQ)
	sub := MsgqSubscriber{Shadow: true, Conflate: true}
	sub.Init(subQ)

	const messages = 200000
	var done atomic.Bool
	go func() {
		defer done.Store(true)
		for i := range messages {
			// varied sizes so cycles never align on the same offsets
			size := 200 + (i%37)*100
			pub.Send(uniform(size, byte(i%251)+1))
		}
	}()

	reads := 0
	for !done.Load() {
		msg := sub.Read()
		if msg == nil {
			continue
		}
		reads++
		marker := msg[0]
		for i, b := range msg {
			if b != marker {
				t.Fatalf("torn read detected at byte %d: marker %d, got %d (message %d bytes)",
					i, marker, b, len(msg))
			}
		}
	}
	if reads == 0 {
		t.Fatalf("stress reader never completed a single read")
	}
	t.Logf("stress: %d intact reads out of %d published", reads, messages)

	// drain deadline: reader must still be functional afterwards
	deadline := time.Now().Add(time.Second)
	msg := uniform(300, 0x42)
	pub.Send(msg)
	for {
		if got := sub.Read(); bytes.Equal(got, msg) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reader not functional after stress run")
		}
	}
}
