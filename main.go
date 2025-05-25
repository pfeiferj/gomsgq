package main

import (
  "fmt"
)

func main() {
  m := Msgq{}
  err := m.Init("blah", 1000)
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
  fmt.Println("hello")
}
