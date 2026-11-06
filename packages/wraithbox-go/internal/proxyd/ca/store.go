package ca

import (
	"context"
	"crypto"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/hwkey"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/platform/secretstore"
	"github.com/wraithbox/wraithbox/packages/wraithbox-go/internal/storeitem"
)

// unlocked runs call until it returns anything but ErrLocked, or ctx
// ends. A locked store means closed: the call waits for the unlock and
// tries again every unlockPoll, and the wait is logged once, with the
// item (S15-03-access-rules, rule 7; S09-06-ca-keys, "Restore").
func (a *Authority) unlocked(ctx context.Context, what string, item storeitem.Name, call func() error) error {
	logged := false
	for {
		err := call()
		if !errors.Is(err, secretstore.ErrLocked) {
			if logged {
				a.log.LogAttrs(ctx, slog.LevelInfo, "secret store unlocked, CA key call continues",
					slog.String("call", what), slog.String("item", item.String()))
			}
			return err
		}
		if !logged {
			a.log.LogAttrs(ctx, slog.LevelWarn, "secret store locked, CA key call waits for the unlock",
				slog.String("call", what), slog.String("item", item.String()),
				slog.String("rule", string(storeitem.RuleLocked)))
			logged = true
		}
		t := time.NewTimer(a.unlockPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return errors.Join(err, ctx.Err())
		case <-t.C:
		}
	}
}

func (a *Authority) write(ctx context.Context, item storeitem.Name, value []byte) error {
	return a.unlocked(ctx, "write", item, func() error {
		return a.store.Write(ctx, item, storeitem.RoleCA, value)
	})
}

func (a *Authority) fetch(ctx context.Context, item storeitem.Name) ([]byte, error) {
	var value []byte
	err := a.unlocked(ctx, "fetch", item, func() error {
		var err error
		value, err = a.store.Fetch(ctx, item, storeitem.RoleCA)
		return err
	})
	return value, err
}

// remove deletes item. An item already gone counts as deleted
// (S15-03-access-rules, "Removing CA keys").
func (a *Authority) remove(ctx context.Context, item storeitem.Name) error {
	err := a.unlocked(ctx, "delete", item, func() error {
		return a.store.Delete(ctx, item, storeitem.RoleCA)
	})
	if errors.Is(err, secretstore.ErrNotFound) {
		return nil
	}
	return err
}

// failureRule is the rule of a failed issue or restore: keychain-needs-team
// when the store refused the call for an ad-hoc signed build, and
// ca-key-restore for every other failure (S09-06-ca-keys, "Restore" and
// "Ad-hoc signed builds").
func failureRule(err error) string {
	var se *secretstore.Error
	if errors.As(err, &se) && se.Rule == storeitem.RuleNeedsTeam {
		return RuleNeedsTeam
	}
	return RuleKeyRestore
}

// heldKey is a restored CA key. Sign and close take its lock, so a key
// closed while a leaf is signed fails that signature, and the leaf is
// refused.
type heldKey struct {
	mu     sync.RWMutex
	key    hwkey.Key
	closed bool
}

var errClosed = errors.New("ca: the CA key was closed")

// Public implements crypto.Signer.
func (h *heldKey) Public() crypto.PublicKey { return h.key.Public() }

// Sign implements crypto.Signer.
func (h *heldKey) Sign(r io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return nil, errClosed
	}
	return h.key.Sign(r, digest, opts)
}

func (h *heldKey) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		h.key.Close()
	}
}
