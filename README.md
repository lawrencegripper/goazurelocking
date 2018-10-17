# GoAzureLocking

[![Build Status](https://dev.azure.com/lawrencegripper/goazurelocking2/_apis/build/status/lawrencegripper.goazurelocking?branchName=master)](https://dev.azure.com/lawrencegripper/goazurelocking2/_build/latest?definitionId=2)

## About 

The package provides a simple interface to handle locking between components running in Azure. It requires an Azure Storage account and use a [`Lease`](https://docs.microsoft.com/en-us/rest/api/storageservices/lease-container) on [`storage containers`](https://docs.microsoft.com/en-us/rest/api/storageservices/create-container) behind the scenes. 

## Using

Create a `LockInstance` for a lock named `lock1`

```golang

lock, err := goazurelocking.NewLockInstance(ctx, "azureStorageAccountNameHere", "azureStorageKeyHere", "lock1", time.Duration(time.Second*5))
if err != nil {
	panic(err)
}

```

Obtain a lock on the `LockInstance` named `lock1` then release it when your done... simple as that!

```golang

// Get the lock
err = lock.Lock()
if err != nil {
	panic("Failed to get lock: %+v", err)
}
fmt.Printf("Acquired lease: %s", lock.LockID.String())

// Release the lock
err = lock.Unlock()
if err != nil {
	panic(err)
}
   
```


## Advanced

You can define custom behaviors which change how locking behaves, by default the following behaviors are used:

- PanicOnLostLock
- AutoRenewLocks
- UnlockWhenContextCancelled

You can create your own and pass them into the `NewLockInstance`. Beware, when you pass in a behavior all default behaviors are ignored. 

To see an example of a behavior look at [AutoRenewLock](https://github.com/lawrencegripper/goazurelocking/blob/master/locking.go#L37)

