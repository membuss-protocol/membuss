//go:build !windows

package main

import (
	"io"
	"os"
)

func openSimulatedRunningExecutable(path string) (io.Closer, error) {
	return os.Open(path)
}
