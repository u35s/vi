package main

import "unsafe"

func BytesToStr(bts []byte) string {
	return *(*string)(unsafe.Pointer(&bts))
}

func StrToBytes(s string) []byte {
	var bs = (*[2]uintptr)(unsafe.Pointer(&s))
	var b = [3]uintptr{bs[0], bs[1], bs[1]}
	return *((*[]byte)(unsafe.Pointer(&b)))
}

func TernaryInt(cond bool, a, b int) int {
	if cond {
		return a
	}
	return b
}

func DoWhile(exec func(), stop func() bool) {
	for {
		exec()
		if stop() {
			break
		}
	}
}
