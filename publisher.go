package main

import (
	"unsafe"
)

type MsgqPublisher struct {
  Msgq Msgq
  Uid uint64
  Id uint64
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
		panic("We are not the active Msgq publisher, panic")
	}
	totalSize := int64(len(data) + 8) + align(int64(len(data)))
	if totalSize * 3 >= p.Msgq.Size {
		panic("Msgq size too small, panic")
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
