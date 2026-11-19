//go:build !linux

package main

import "os/exec"

func detach(*exec.Cmd) {}
