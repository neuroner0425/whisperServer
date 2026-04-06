// runtime.go stores the process-wide runtime instance owned by server bootstrap.
package server

import (
	intruntime "whisperserver/src/internal/runtime"
)

var appRuntime *intruntime.Runtime

// eventBroker exposes the runtime broker to HTTP wiring without leaking bootstrap details.
func eventBroker() *intruntime.Broker {
	if appRuntime == nil {
		return nil
	}
	return appRuntime.Broker()
}
