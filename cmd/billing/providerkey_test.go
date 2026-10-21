package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/0gfoundation/0g-sandbox/internal/tee"
)

func TestKeepProviderKeyListed(t *testing.T) {
	want := common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")
	other := common.HexToAddress("0x01")

	// Rounds: an error, a different key, then the provider listed again
	// (a tapp-server restart dropped it; asking again restores it).
	answers := []func() (*tee.ProviderKey, error){
		func() (*tee.ProviderKey, error) { return nil, errors.New("kms unreachable") },
		func() (*tee.ProviderKey, error) { return &tee.ProviderKey{Address: other, InEvidence: true}, nil },
		func() (*tee.ProviderKey, error) { return &tee.ProviderKey{Address: want, InEvidence: true}, nil },
	}
	var calls atomic.Int32
	done := make(chan struct{})
	fetch := func(context.Context) (*tee.ProviderKey, error) {
		n := int(calls.Add(1)) - 1
		if n >= len(answers) {
			<-done
			return nil, errors.New("finished")
		}
		pk, err := answers[n]()
		if n == len(answers)-1 {
			defer close(done)
		}
		return pk, err
	}

	core, logs := observer.New(zap.WarnLevel)
	var inEvidence atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go keepProviderKeyListed(ctx, want, fetch, time.Millisecond, &inEvidence, zap.New(core))

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh loop did not run three rounds")
	}
	// Give the last round's Store a moment after fetch returned.
	deadline := time.Now().Add(time.Second)
	for !inEvidence.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()

	if !inEvidence.Load() {
		t.Fatal("a refresh that found the key listed did not record it")
	}
	if logs.FilterMessageSnippet("refresh failed").Len() == 0 {
		t.Error("fetch error not logged")
	}
	mismatch := logs.FilterMessageSnippet("different provider key")
	if mismatch.Len() == 0 || mismatch.All()[0].Level != zap.ErrorLevel {
		t.Error("a different KMS key must be logged as an error")
	}
}
