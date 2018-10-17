package locking

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/Azure/azure-storage-blob-go/2016-05-31/azblob"
	"github.com/cenkalti/backoff"
	"github.com/satori/go.uuid"
)

type (
	// Lock represents the status of a lock
	Lock struct {
		ctx           context.Context
		used          bool
		lockAcquired  bool
		Cancel        context.CancelFunc
		panic         func(string)
		LockTTL       time.Duration
		LockLost      chan struct{}
		LockID        uuid.UUID
		Lock          func() error
		Renew         func() error
		Unlock        func() error
		unlockContext func(context.Context) error
	}

	// BehaviorFunc is a type converter that allows a func to be used as a `Behavior`
	BehaviorFunc func(*Lock) *Lock
)

var (
	defaultLockBehaviors = []BehaviorFunc{AutoRenewLock, PanicOnLostLock, UnlockWhenContextCancelled, RetryObtainingLock}

	// AutoRenewLock configures the lock to autorenew itself
	AutoRenewLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					// Context has been cancelled, exit so can be gc'd
					return
				case <-time.Tick(l.LockTTL / 2):
					// If the 'lock' function hasn't been used yet spin
					if !l.lockAcquired {
						continue
					}
					// Do a renew. If we fail, clean up and notify that the lock is lost
					err := l.Renew()
					if err != nil {
						l.Cancel()
						l.LockLost <- struct{}{}
						return
					}
				case <-l.LockLost:
					return
				}
			}
		}()
		return l
	})

	// RetryObtainingLock configures the lock to retry getting a lock if it is already held
	RetryObtainingLock = BehaviorFunc(func(l *Lock) *Lock {
		// Assuming locks will be initialised with roughly
		// the correct TTL required to perform the operation
		// lets give it 10x time to acquire it
		obtainLockBackoffPolicy := backoff.NewExponentialBackOff()
		obtainLockBackoffPolicy.MaxElapsedTime = l.LockTTL * 10
		existingLockFunc := l.Lock

		// Replace existing lock function with exponential retrying one
		l.Lock = func() error { return backoff.Retry(existingLockFunc, obtainLockBackoffPolicy) }

		return l
	})

	// UnlockWhenContextCancelled will remove a lease when a context is cancelled
	UnlockWhenContextCancelled = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					// If the 'lock' function wasn't ever called don't worry
					if !l.lockAcquired {
						return
					}
					// The original context is dead but we don't want to leave the lock in place
					// so lets create a new context and give it 3 seconds to get the job done
					ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*5))
					defer cancel()
					l.unlockContext(ctx) //nolint: errcheck

					return
				case <-l.LockLost:
					return
				}
			}
		}()
		return l
	})

	// PanicOnLostLock configures the lock to autorenew itself
	PanicOnLostLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			select {
			case <-l.ctx.Done():
				return
			case <-l.LockLost:
				l.Cancel()
				l.panic("Lock lost and 'PanicOnLostLock' set")
			}
		}()
		return l
	})
)

// NewLockInstance returns a new instance of a lock
func NewLockInstance(ctxParent context.Context, accountName, accountKey, lockName string, lockTTL time.Duration, behavior ...BehaviorFunc) (*Lock, error) {
	if accountKey == "" {
		return nil, fmt.Errorf("Empty accountKey is invalid")
	}
	if accountName == "" {
		return nil, fmt.Errorf("Empty accountName is invalid")
	}
	if lockTTL.Seconds() < 15 || lockTTL.Seconds() > 60 {
		return nil, fmt.Errorf("LockTTL of %v seconds is outside allowed range of 15-60seconds", lockTTL.Seconds())
	}

	creds := azblob.NewSharedKeyCredential(accountName, accountKey)

	// Create a ContainerURL object to a container
	u, _ := url.Parse(fmt.Sprintf("https://%s.blob.core.windows.net/%s", accountName, lockName))
	containerURL := azblob.NewContainerURL(*u, azblob.NewPipeline(creds, azblob.PipelineOptions{}))
	_, err := containerURL.Create(ctxParent, nil, azblob.PublicAccessNone)

	// Create will return a '409' response code if the container already exists
	// we only error on other conditions as it's expected that a lock of this
	// name may already exist
	if err != nil && err.(azblob.ResponseError) != nil && err.(azblob.ResponseError).Response().StatusCode != 409 {
		return nil, err
	}

	// Create our own context which will be cancelled independantly of
	// the parent context
	ctx, cancel := context.WithCancel(ctxParent)

	lockInstance := &Lock{
		ctx:      ctx,
		Cancel:   cancel,
		panic:    func(s string) { panic(s) },
		LockTTL:  lockTTL,
		LockLost: make(chan struct{}, 1),
		LockID:   uuid.NewV4(),
	}

	lockInstance.unlockContext = func(ctx context.Context) error {
		// Mark this lock instance as used to prevent reuse
		// as the library doesn't handle multiple uses per lock instance
		lockInstance.used = true

		_, err := containerURL.ReleaseLease(ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})
		// No matter what happened cancel the context to close off the go routines running in behaviors
		lockInstance.Cancel()

		if err != nil {
			return err
		}

		return nil
	}

	lockInstance.Unlock = func() error {
		return lockInstance.unlockContext(lockInstance.ctx)
	}

	lockInstance.Lock = func() error {
		if lockInstance.used {
			return fmt.Errorf("Lock instance already used, cannot be reused")
		}
		_, err := containerURL.AcquireLease(lockInstance.ctx, lockInstance.LockID.String(), int32(lockTTL.Seconds()), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}

		lockInstance.lockAcquired = true

		return nil
	}

	lockInstance.Renew = func() error {
		if lockInstance.used {
			return fmt.Errorf("Lock instance already used, cannot be reused")
		}
		_, err := containerURL.RenewLease(lockInstance.ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}
		return nil
	}

	// If behaviors haven't been defined use the defaults
	if len(behavior) == 0 {
		behavior = defaultLockBehaviors
	}

	// Configure behaviors
	for _, b := range behavior {
		lockInstance = b(lockInstance)
	}

	return lockInstance, nil
}
