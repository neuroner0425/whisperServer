package server

import "os"

const requeueVADFallbackArg = "--requeue-vad-fallback"

func startupVADFallbackRequeueEnabled() bool {
	for _, arg := range os.Args[1:] {
		if arg == requeueVADFallbackArg {
			return true
		}
	}
	return false
}
