package report

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestExitCodes(t *testing.T) {
	for _, c := range []int{2, 3, 4} {
		e := Fail(c, "phase", errors.New("failure"))
		if ExitCode(Fail(3, "outer", e)) != c {
			t.Fatal("lost error code")
		}
	}
	if ExitCode(fmt.Errorf("wrapped: %w", context.Canceled)) != 130 {
		t.Fatal("lost cancellation")
	}
	if ExitCode(nil) != 0 {
		t.Fatal("success code")
	}
}
