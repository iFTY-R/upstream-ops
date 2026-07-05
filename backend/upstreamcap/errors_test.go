package upstreamcap

import (
	"errors"
	"testing"
)

func TestNormalizeErrorClassifiesUnauthorized(t *testing.T) {
	err := NormalizeError(7, CapAPIKeys, errors.New("newapi api keys: HTTP 401 unauthorized"))
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %T, want CapabilityError", err)
	}
	if capErr.Code != ErrUpstreamUnauthorized || capErr.Temporary {
		t.Fatalf("cap err = %#v, want unauthorized permanent", capErr)
	}
	if capErr.ChannelID != 7 || capErr.Capability != CapAPIKeys {
		t.Fatalf("cap err context = %#v", capErr)
	}
}

func TestNormalizeErrorClassifiesTimeoutTemporary(t *testing.T) {
	err := NormalizeError(8, CapOpenAIProbe, errors.New("context deadline exceeded"))
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %T, want CapabilityError", err)
	}
	if capErr.Code != ErrUpstreamUnreachable || !capErr.Temporary {
		t.Fatalf("cap err = %#v, want unreachable temporary", capErr)
	}
}

func TestUnsupported(t *testing.T) {
	err := Unsupported(9, CapSubscription)
	var capErr *CapabilityError
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %T, want CapabilityError", err)
	}
	if capErr.Code != ErrCapabilityUnsupported || capErr.Temporary {
		t.Fatalf("cap err = %#v, want unsupported permanent", capErr)
	}
}
