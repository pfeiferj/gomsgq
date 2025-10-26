package gomsgq

// #include <stdint.h>
// #include <signal.h>
//
// static void thread_signal(unsigned int tid) {
//   #ifndef SYS_tkill
//     // TODO: this won't work for multithreaded programs
//     kill(tid, SIGUSR2);
//   #else
//     syscall(SYS_tkill, tid, SIGUSR2);
//   #endif
// }
import "C"

func ThreadSignal(tid uint32) {
	C.thread_signal(C.uint(tid));
}
