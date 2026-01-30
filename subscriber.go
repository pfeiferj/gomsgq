package gomsgq

import (
	"unsafe"
	"math/rand/v2"
	"os"

	"sync/atomic"
)

type MsgqSubscriber struct {
	Shadow bool
  Msgq Msgq
  Uid uint64
  Id uint64
	Conflate bool
	shadowPointer uint64
}

func generateUid() uint64 {
  return uint64(rand.Uint32()) << 32 | uint64(os.Getpid())
}

func (s *MsgqSubscriber) Init(msgq Msgq) {
  s.Msgq = msgq
  s.Uid = generateUid()
	if !s.Shadow {
		for {
			curNumReaders := *s.Msgq.Header.NumReaders
			newNumReaders := curNumReaders + 1
			if (newNumReaders > NUM_READERS) {
				*s.Msgq.Header.NumReaders = 0
				
				for i := range NUM_READERS {
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
	}
  s.Reset()
}

func (s *MsgqSubscriber) Reset() {
	if !s.Shadow {
		s.Msgq.Header.ReadValids[s.Id] = 1
		s.Msgq.Header.ReadPointers[s.Id] = *s.Msgq.Header.WritePointer
	} else {
		s.shadowPointer = *s.Msgq.Header.WritePointer
	}
}

func (s *MsgqSubscriber) Ready() bool {
	if !s.Shadow {
		for (s.Uid != s.Msgq.Header.ReadUids[s.Id]) {
			s.Init(s.Msgq)
		}

		for(s.Msgq.Header.ReadValids[s.Id] == 0) {
			s.Reset()
		}

		readPointer := s.Msgq.Header.ReadPointers[s.Id]
		readPointer &= 0xFFFFFFFF

		writePointer := *s.Msgq.Header.WritePointer
		writePointer &= 0xFFFFFFFF

		return readPointer != writePointer
	} else {
		readPointer := s.shadowPointer
		readPointer &= 0xFFFFFFFF

		writePointer := *s.Msgq.Header.WritePointer
		writePointer &= 0xFFFFFFFF
		return readPointer != writePointer
	}
}

func (s *MsgqSubscriber) Read() []byte {
	if !s.Ready() {
		return nil
	}

	for {
		var readPointer uint64
		if s.Shadow {
			readPointer = s.shadowPointer
		} else {
			readPointer = s.Msgq.Header.ReadPointers[s.Id]
		}
		readCycles := readPointer >> 32
		readPointer &= 0xFFFFFFFF
		if s.Shadow {
			writePointer := *s.Msgq.Header.WritePointer
			writeCycles := writePointer >> 32
			writePointer &= 0xFFFFFFFF
			if readPointer > writePointer && readCycles != writeCycles {
				s.Reset()
				continue
			}
		}
		size := *(*int64) (unsafe.Pointer(&s.Msgq.Data[readPointer]))

		if size == -1 {
			readCycles++
			if s.Shadow {
				s.shadowPointer = (readCycles << 32)
			} else {
				s.Msgq.Header.ReadPointers[s.Id] = (readCycles << 32)
			}
			continue
		}

		if size >= s.Msgq.Size || size <= 0 {
			panic("Invalid Msgq message size")
		}

		nextReadPointer := readPointer + 8 + uint64(size) + uint64(align(size))
		if s.Conflate {
			writePointer := *s.Msgq.Header.WritePointer
			writePointer &= 0xFFFFFFFF
			if nextReadPointer != writePointer {
				if s.Shadow {
					s.shadowPointer = (readCycles << 32) | nextReadPointer
				} else {
					s.Msgq.Header.ReadPointers[s.Id] = (readCycles << 32) | nextReadPointer
				}
				continue
			}
		}

		err := s.Msgq.Mem.Flush()
		if err != nil {
			panic("Msgq Flush Error")
		}
		result := make([]byte, size)
		for i := range size {
			result[i] = s.Msgq.Data[int64(readPointer) + 8 + i]
		}
		err = s.Msgq.Mem.Flush()
		if err != nil {
			panic("Msgq Flush Error")
		}
		
		if s.Shadow {
			s.shadowPointer = (readCycles << 32) | nextReadPointer
		} else {
			s.Msgq.Header.ReadPointers[s.Id] = (readCycles << 32) | nextReadPointer
		}

		if !s.Shadow && s.Msgq.Header.ReadValids[s.Id] == 0 {
			s.Reset()
			continue
		}

		return result
	}
}
