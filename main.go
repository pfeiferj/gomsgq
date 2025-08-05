package main

import (
  "fmt"
	"time"
)

func main() {
  m := Msgq{}
  err := m.Init("test", 100)
  defer m.Close()
  if err != nil {
    fmt.Println(err.Error())
    return
  }
  //*m.Header.NumReaders = 12
  //*m.Header.WriteUid = 85
  //m.Header.ReadUids[0] = 2
  fmt.Println(*m.Header.NumReaders)
  fmt.Println(*m.Header.WriteUid)
  fmt.Println(m.Header.ReadUids[0])

  err = m.Mem.Flush()
  if err != nil {
    fmt.Println(err.Error())
    return
  }

	pub := MsgqPublisher{}
	pub.Init(m)
	m.WaitForSubscriber()
	fmt.Println("Num readers", *pub.Msgq.Header.NumReaders)
	for i := range 1000 {
		pub.Send([]byte(fmt.Sprintf("Hello %v", i)))
		time.Sleep(1 * time.Second)
	}
  fmt.Println("hello")
}
