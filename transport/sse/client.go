// Package sse streams Sap records over server-sent events.
package sse

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Stream connects to an SSE endpoint and emits records and connection states.
func Stream(ctx context.Context, url string) (<-chan RecordMessage, <-chan ConnectionState) {
	records := make(chan RecordMessage, 256)
	states := make(chan ConnectionState, 32)
	go func() {
		defer close(records)
		defer close(states)

		attempt := 0
		client := &http.Client{Timeout: 0}
		for {
			select {
			case <-ctx.Done():
				states <- ConnectionState{State: StateDisconnected}
				return
			default:
			}

			attempt++
			states <- ConnectionState{State: StateConnecting, Attempt: attempt}
			if err := streamOnce(ctx, client, url, records, states, attempt); err != nil {
				if ctx.Err() != nil {
					states <- ConnectionState{State: StateDisconnected}
					return
				}
				retryAt := time.Now().Add(2 * time.Second)
				states <- ConnectionState{State: StateRetrying, Attempt: attempt, Err: err.Error(), RetryAt: retryAt}
				timer := time.NewTimer(time.Until(retryAt))
				select {
				case <-ctx.Done():
					timer.Stop()
					states <- ConnectionState{State: StateDisconnected}
					return
				case <-timer.C:
				}
				continue
			}
			states <- ConnectionState{State: StateDisconnected}
			return
		}
	}()
	return records, states
}

func streamOnce(ctx context.Context, client *http.Client, url string, records chan<- RecordMessage, states chan<- ConnectionState, attempt int) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close response body: %w", closeErr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	states <- ConnectionState{State: StateConnected, Attempt: attempt}

	scanner := bufio.NewScanner(resp.Body)
	statesent := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, ":") && !statesent {
			statesent = true
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var record sapv1.Record
		if err := protojson.Unmarshal([]byte(line[len("data: "):]), &record); err != nil {
			continue
		}
		records <- RecordMessage{Record: &record}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("stream closed")
}
