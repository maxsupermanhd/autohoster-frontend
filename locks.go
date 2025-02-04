package main

import "sync"

var (
	dbNameCreationLock = sync.Mutex{}
	dbRegisterLock     = sync.Mutex{}
)
