//go:build minprofile

package eventbus

import (
	"errors"
	"testing"
)

func TestFromEnv_MinProfileRejectsKafka(t *testing.T) {
	t.Setenv("EVENTBUS_PROVIDER", "kafka")
	_, err := FromEnv("localhost:9092", nil)
	if !errors.Is(err, ErrKafkaNotCompiled) {
		t.Fatalf("kafka selection on minprofile: %v", err)
	}
}
