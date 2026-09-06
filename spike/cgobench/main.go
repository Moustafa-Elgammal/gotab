package main

/*
#include <stdint.h>
static int64_t noop(int64_t x) { return x + 1; }
extern int64_t goCallback(int64_t);
static int64_t callBackNTimes(int64_t n) {
    int64_t s = 0;
    for (int64_t i = 0; i < n; i++) s += goCallback(i);
    return s;
}
*/
import "C"
import (
	"fmt"
	"time"
)

//export goCallback
func goCallback(x C.int64_t) C.int64_t { return x + 1 }

//go:noinline
func goNoop(x int64) int64 { return x + 1 }

func main() {
	const N = 3_000_000
	var s int64
	t := time.Now()
	for i := int64(0); i < N; i++ {
		s += goNoop(i)
	}
	goDur := time.Since(t)

	var s2 C.int64_t
	t = time.Now()
	for i := int64(0); i < N; i++ {
		s2 += C.noop(C.int64_t(i))
	}
	cgoDur := time.Since(t)

	t = time.Now()
	s3 := C.callBackNTimes(C.int64_t(N))
	cbDur := time.Since(t)

	fmt.Printf("N = %d  (checksums %d %d %d)\n", N, s, int64(s2), int64(s3))
	fmt.Printf("  pure Go call    : %7.2f ns/op\n", float64(goDur.Nanoseconds())/N)
	fmt.Printf("  Go -> C  (cgo)  : %7.2f ns/op  (%.1fx)\n", float64(cgoDur.Nanoseconds())/N, float64(cgoDur)/float64(goDur))
	fmt.Printf("  C  -> Go (cb)   : %7.2f ns/op  (%.1fx)\n", float64(cbDur.Nanoseconds())/N, float64(cbDur)/float64(goDur))
}
