// main.go is the process entrypoint for the HTTP server binary.
package main

import "whisperserver/src/internal/server"

// main delegates the full bootstrap and lifecycle management to internal/server.
func main() {
	server.Run()
}
