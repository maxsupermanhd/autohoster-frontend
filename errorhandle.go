package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/jackc/pgx/v5"
)

func DBErr(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, pgx.ErrNoRows) {
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		logoutHandler(w, r)
		return true
	}
	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
	notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
	return true
}
