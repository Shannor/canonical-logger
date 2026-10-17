package cloggcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriter_NilLogger(t *testing.T) {
	writer := &Writer{Logger: nil}
	data := []byte(`{"message":"test","severity":"INFO"}`)
	n, err := writer.Write(data)
	assert.NoError(t, err)
	assert.Equal(t, len(data), n)

	invalidJSON := []byte(`not json`)
	n, err = writer.Write(invalidJSON)
	assert.NoError(t, err)
	assert.Equal(t, len(invalidJSON), n)
}
