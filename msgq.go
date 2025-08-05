package main

import (
	"errors"
	"math/rand/v2"
	"os"
	"unsafe"
	"fmt"

	"sync/atomic"

	"github.com/edsrzf/mmap-go"
)

var OPENPILOT_PREFIX = os.Getenv("OPENPILOT_PREFIX")
const PATH_PREFIX = "/dev/shm/"
const NUM_READERS = 15
var HEADER_SIZE = (3 * 8 + 3 * NUM_READERS * 8) + align(3 * 8 + 3 * NUM_READERS * 8)
type MsgqPublisher struct {
  Msgq Msgq
  Uid uint64
  Id uint64
}

type MsgqSubscriber struct {
  Msgq Msgq
  Uid uint64
  Id uint64
}

func align(length int64) int64 {
	remainder := length % 8
	if remainder == 0 {
		return 0
	}
	return 8 - remainder
}

func (p *MsgqPublisher) Init(msgq Msgq) {
  p.Msgq = msgq
  p.Uid = generateUid()

	*p.Msgq.Header.NumReaders = 0
	*p.Msgq.Header.WriteUid = p.Uid
	*p.Msgq.Header.WritePointer = 0

	for i := range NUM_READERS {
		p.Msgq.Header.ReadValids[i] = 0
		p.Msgq.Header.ReadUids[i] = 0
  }
}

func (p *MsgqPublisher) Send(data []byte) {
	if p.Uid != *p.Msgq.Header.WriteUid {
		fmt.Printf("We are not the active publisher, panic")
		panic(-1)
	}
	totalSize := int64(len(data) + 8) + align(int64(len(data)))
	if totalSize * 3 >= p.Msgq.Size {
		fmt.Printf("Queue too small, panic")
		panic(-1)
	}
	numReaders := *p.Msgq.Header.NumReaders
	writePointer := *p.Msgq.Header.WritePointer
	writeCycles := writePointer >> 32
	writePointer &= 0xFFFFFFFF
	remainingSpace := p.Msgq.Size - int64(writePointer) - totalSize - 8

	// Invalidate all readers that are beyond the write pointer
	if remainingSpace <= 0 {
		// write -1 size tag indicating wraparound
		*(*int64)(unsafe.Pointer(&p.Msgq.Data[writePointer])) = int64(-1)
		for i := range numReaders {
			readPointer := p.Msgq.Header.ReadPointers[i]
			readCycles := readPointer >> 32
			readPointer &= 0xFFFFFFFF
			if readPointer > writePointer && readCycles != writeCycles {
				p.Msgq.Header.ReadValids[i] = 0 //false
			}
		}
		writePointer = 0
		writeCycles += 1
		*p.Msgq.Header.WritePointer = (writeCycles << 32) | writePointer
	}

  // Invalidate readers that are in the area that will be written
	end := writePointer + uint64(totalSize)
	for i := range numReaders {
		readPointer := p.Msgq.Header.ReadPointers[i]
		readCycles := readPointer >> 32
		readPointer &= 0xFFFFFFFF

		if readPointer >= writePointer && readPointer < end && readCycles != writeCycles {
			p.Msgq.Header.ReadValids[i] = 0 //false
		}
	}
	
  // Write size tag
	*(*int64) (unsafe.Pointer(&p.Msgq.Data[writePointer])) = int64(len(data))

  // Copy data
	for i, b := range data {
		p.Msgq.Data[int(writePointer) + 8 + i] = b
	}

  // Update write pointer
	writePointer += uint64(totalSize)
	*p.Msgq.Header.WritePointer = (writeCycles << 32) | (writePointer & 0xFFFFFFFF)

  // Notify readers
	for i := range numReaders {
		rUid := p.Msgq.Header.ReadUids[i]
		ThreadSignal(uint32(rUid))
	}
}

func generateUid() uint64 {
  return uint64(rand.Uint32()) << 32 | uint64(os.Getpid())
}

func (s *MsgqSubscriber) Init(msgq Msgq) {
  s.Msgq = msgq
  s.Uid = generateUid()
  for true {
    curNumReaders := *s.Msgq.Header.NumReaders
    newNumReaders := curNumReaders + 1
    if (newNumReaders > NUM_READERS) {
      *s.Msgq.Header.NumReaders = 0
      
      for i := 0; i < NUM_READERS; i++ {
        s.Msgq.Header.ReadValids[i] = 0

        old_uid := s.Msgq.Header.ReadUids[i]
        s.Msgq.Header.ReadUids[i] = 0

        ThreadSignal(uint32(old_uid & 0xFFFFFFFF))
      }
      continue
    }
    if atomic.CompareAndSwapUint64(s.Msgq.Header.NumReaders, curNumReaders, newNumReaders) {
      s.Id = curNumReaders
      s.Msgq.Header.ReadValids[curNumReaders] = 0
      s.Msgq.Header.ReadPointers[curNumReaders] = 0
      s.Msgq.Header.ReadUids[curNumReaders] = s.Uid
      break
    }
  }
  s.Reset()
}

func (s *MsgqSubscriber) Reset() {
  s.Msgq.Header.ReadValids[s.Id] = 1
  s.Msgq.Header.ReadPointers[s.Id] = *s.Msgq.Header.WritePointer
}

type Msgq struct {
  Size int64
  Path string
  File *os.File
  Mem mmap.MMap
	Data []uint8
  Header Header
}

type Header struct {
  NumReaders *uint64
  WritePointer *uint64
  WriteUid *uint64
  ReadPointers []uint64
  ReadValids []uint64
  ReadUids []uint64
}

func (h *Header) Init(m mmap.MMap) {
  h.NumReaders = (*uint64)(unsafe.Pointer(&m[0]))
  h.WritePointer = (*uint64)(unsafe.Pointer(&m[8]))
  h.WriteUid = (*uint64)(unsafe.Pointer(&m[16]))
  readPointers := unsafe.Slice((*uint64)(unsafe.Pointer(&m[24])), NUM_READERS)
  h.ReadPointers = readPointers
  readValids := unsafe.Slice((*uint64)(unsafe.Pointer(&m[24 + NUM_READERS * 8])), NUM_READERS)
  h.ReadValids = readValids
  readUids := unsafe.Slice((*uint64)(unsafe.Pointer(&m[24 + 2*NUM_READERS * 8])), NUM_READERS)
  h.ReadUids = readUids
}


func (m *Msgq) Close() (error, error) {
  var memErr error = nil
  var fileErr error = nil
  if m.Mem != nil {
    memErr = m.Mem.Unmap()
  }
  if m.File != nil {
    fileErr = m.File.Close()
  }
  return memErr, fileErr
}

func (m *Msgq) Init(path string, size int64) error {
  if(size >= 0xFFFFFFFF) {
    return errors.New("Buffer must be smaller than 2^32 bytes")
  }
  m.Path = path
  m.Size = size

  fullPath := PATH_PREFIX 
  if OPENPILOT_PREFIX != "" {
    fullPath = fullPath + OPENPILOT_PREFIX
  }
  fullPath = fullPath + path
  f, err := os.OpenFile(fullPath, os.O_RDWR | os.O_CREATE, 0664)
  if err != nil {
    return err
  }
  err = f.Truncate(size + int64(HEADER_SIZE))
  if err != nil {
    return err
  }
  err = f.Sync()
  if err != nil {
    return err
  }
  mem, err := mmap.Map(f, mmap.RDWR, 0)
  if err != nil {
    return err
  }
  m.Mem = mem
  m.Header = Header{}
  m.Header.Init(mem)
  data := unsafe.Slice((*byte)(unsafe.Pointer(&mem[HEADER_SIZE])), size)
	m.Data = data

  return nil
}

func (m *Msgq) WaitForSubscriber() {
	for *m.Header.NumReaders == 0 {}
}
