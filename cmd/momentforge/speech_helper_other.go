//go:build !darwin

package main

// On other systems there is no Apple speech helper; the engine says so and
// whisper.cpp is the way to transcribe.
var speechHelper []byte
