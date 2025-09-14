package gomsgq

import (
	"errors"
	"os"
	"unsafe"

	"github.com/edsrzf/mmap-go"
)

var OPENPILOT_PREFIX = os.Getenv("OPENPILOT_PREFIX")
const PATH_PREFIX = "/dev/shm/"
const NUM_READERS = 15
var HEADER_SIZE = (3 * 8 + 3 * NUM_READERS * 8) + align(3 * 8 + 3 * NUM_READERS * 8)

type Msgq struct {
  Size int64
  Path string
  File *os.File
  Mem mmap.MMap
	Data []uint8
  Header Header
}

func align(length int64) int64 {
	remainder := length % 8
	if remainder == 0 {
		return 0
	}
	return 8 - remainder
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
    return errors.New("buffer must be smaller than 2^32 bytes")
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
	for *m.Header.NumReaders == 0 {
		err := m.Mem.Flush()
		if err != nil {
			panic("Msgq failed to flush")
		}
	}
}

