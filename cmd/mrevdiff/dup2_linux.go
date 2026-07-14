//go:build linux

package main

import "syscall"

// linux/arm64 has no dup2 syscall, so Go's syscall package only exposes Dup3
// there; Dup3 with no flags is exactly dup2 on every linux arch.
func dup2(oldfd, newfd int) error { return syscall.Dup3(oldfd, newfd, 0) }
