package main

import (
	"errors"
	"math/rand/v2"
	"os"
	"syscall"
	"unsafe"

	"sync/atomic"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/unix"
)

var OPENPILOT_PREFIX = os.Getenv("OPENPILOT_PREFIX")
const PATH_PREFIX = "/tmp/"
const NUM_READERS = 15
const HEADER_SIZE = (3 * 8 + 3 * NUM_READERS * 8)

type MsgqPublisher struct {}
type MsgqSubscriber struct {
  Msgq Msgq
  Uid uint64
  Id uint64
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

        // TODO: probably need to support tkill
        unix.Kill(int(old_uid & 0xFFFFFFFF), syscall.SIGUSR2)
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
  err = f.Truncate(size)
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

  return nil
}
