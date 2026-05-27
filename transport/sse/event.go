package sse

import (
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
)

// ConnectionState describes the current state of an SSE client connection.
type ConnectionState struct {
	State   string
	Attempt int
	Err     string
	RetryAt time.Time
}

// State constants describe the SSE client connection lifecycle.
const (
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateRetrying     = "retrying"
	StateDisconnected = "disconnected"
)

// RecordMessage carries a streamed Sap record.
type RecordMessage struct {
	Record *sapv1.Record
}
