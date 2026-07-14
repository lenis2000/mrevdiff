//go:build !darwin && !linux

package main

import "errors"

func dup2(oldfd, newfd int) error {
	return errors.New("stderr redirection not supported on this platform")
}
