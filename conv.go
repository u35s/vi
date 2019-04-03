package main

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
