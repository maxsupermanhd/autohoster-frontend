package main

import "sync"

var (
	dbNameCreationLock = sync.Mutex{}
)
