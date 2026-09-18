package main

import (
	"io"
	"net/http"
	"testing"
)

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
