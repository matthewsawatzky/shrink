package main

import (
	"context"
	"io"
)

func testApplication() *application {
	return newApplication(context.Background(), io.Discard, io.Discard)
}
