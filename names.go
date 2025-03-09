package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func namesHandler(w http.ResponseWriter, r *http.Request) {
	if !checkUserAuthorized(r) {
		basicLayoutLookupRespond(templateNotAuthorized, w, r, map[string]any{})
		return
	}
	and, err := accGetNamesData(r.Context(), sessionGetUserID(r))
	if DBErr(w, r, err) {
		return
	}
	basicLayoutLookupRespond("names", w, r, map[string]any{
		"and": and,
	})
}

func namesHandlerPOST(w http.ResponseWriter, r *http.Request) {
	if !checkUserAuthorized(r) {
		basicLayoutLookupRespond(templateNotAuthorized, w, r, map[string]any{})
		return
	}
	and, err := accGetNamesData(r.Context(), sessionGetUserID(r))
	if DBErr(w, r, err) {
		return
	}
	nameID := parseFormInt(r, "nameID")
	if nameID == nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Invalid name ID"})
		return
	}
	action := r.FormValue("action")
	if action != "select" {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Action can be only 'select'"})
		return
	}
	nameFound := false
	for _, v := range and.Names {
		if v.ID == *nameID {
			nameFound = true
			break
		}
	}
	if !nameFound {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Name not found"})
		return
	}
	tag, err := dbpool.Exec(r.Context(), `update accounts set name = $1 where id = $2`, *nameID, sessionGetUserID(r))
	if DBErr(w, r, err) {
		return
	}
	if !tag.Update() || tag.RowsAffected() != 1 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
	}
	namesHandler(w, r)
}

func namePickHandler(w http.ResponseWriter, r *http.Request) {
	if !checkUserAuthorized(r) {
		basicLayoutLookupRespond(templateNotAuthorized, w, r, map[string]any{})
		return
	}
	dbNameCreationLock.Lock()
	defer dbNameCreationLock.Unlock()
	and, err := accGetNamesData(r.Context(), sessionGetUserID(r))
	if DBErr(w, r, err) {
		return
	}
	if !and.allowedToCreateName() {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You can not create a new name right now."})
		return
	}
	if r.Method == "GET" {
		basicLayoutLookupRespond("namepick", w, r, map[string]any{
			"and": and,
		})
		return
	}

	clearNameCharset := `abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-~`
	_ = clearNameCharset

	subDisplayName := r.FormValue("displayName")
	subClearName := r.FormValue("clearName")

	if !and.hasClearName(subClearName) {
		if !and.allowedToCreateClearName() {
			basicLayoutLookupRespond("namepick", w, r, map[string]any{
				"rejected":         "You can not create any more clear names",
				"and":              and,
				"retryDisplayName": subDisplayName,
				"retryClearName":   subClearName,
			})
			return
		}
	}

	if len(subDisplayName) > 25 || len(subDisplayName) < 3 {
		basicLayoutLookupRespond("namepick", w, r, map[string]any{
			"rejected":         "Display name length must be between 2 and 26 symbols",
			"and":              and,
			"retryDisplayName": subDisplayName,
			"retryClearName":   subClearName,
		})
		return
	}
	if len(subClearName) > 25 || len(subClearName) < 3 {
		basicLayoutLookupRespond("namepick", w, r, map[string]any{
			"rejected":         "Clear name length must be between 2 and 26 symbols",
			"and":              and,
			"retryDisplayName": subDisplayName,
			"retryClearName":   subClearName,
		})
		return
	}

	isClearNameValid := true
	for _, c := range subClearName {
		if !strings.ContainsRune(clearNameCharset, c) {
			isClearNameValid = false
			break
		}
	}

	if !isClearNameValid {
		basicLayoutLookupRespond("namepick", w, r, map[string]any{
			"rejected":         "Clear name contains disallowed characters",
			"and":              and,
			"retryDisplayName": subDisplayName,
			"retryClearName":   subClearName,
		})
		return
	}

	if !and.hasClearName(subClearName) {
		var numClearNames int
		if DBErr(w, r, dbpool.QueryRow(r.Context(), `select count(*) from names where lower(clear_name) = lower($1) and status != 'denied'`, subClearName).Scan(&numClearNames)) {
			return
		}
		if numClearNames != 0 {
			basicLayoutLookupRespond("namepick", w, r, map[string]any{
				"rejected":         "Such clear name is already used",
				"and":              and,
				"retryDisplayName": subDisplayName,
				"retryClearName":   subClearName,
			})
			return
		}
	}

	var numDisplayNames int
	if DBErr(w, r, dbpool.QueryRow(r.Context(), `select count(*) from names where display_name = $1 and status != 'denied'`, subDisplayName).Scan(&numDisplayNames)) {
		return
	}
	if numDisplayNames != 0 {
		basicLayoutLookupRespond("namepick", w, r, map[string]any{
			"rejected":         "Such display name is already used",
			"and":              and,
			"retryDisplayName": subDisplayName,
			"retryClearName":   subClearName,
		})
		return
	}

	if DBErr(w, r, pgx.BeginFunc(r.Context(), dbpool, func(tx pgx.Tx) error {
		var insNameID int
		err := tx.QueryRow(r.Context(), `insert into names (account, display_name, clear_name) values ($1, $2, $3) returning id`, sessionGetUserID(r), subDisplayName, subClearName).Scan(&insNameID)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(r.Context(), `update accounts set last_name_change = now(), name = $2 where id = $1`, sessionGetUserID(r), insNameID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 || !tag.Update() {
			notifyErrorWebhook(fmt.Sprintf("sus tag on last name change time update %s\n%s", tag.String(), string(debug.Stack())))
		}
		return nil
	})) {
		return
	}
	modSendWebhook(fmt.Sprintf("User `%s` created name clear:`%s` display:`%s`", sessionGetUsername(r), subClearName, subDisplayName))
	namesHandler(w, r)
}

type accountName struct {
	ID          int
	ClearName   string
	DisplayName string
	TimeCreated time.Time
	Status      string
	Note        string
	Selected    bool
}

type accountNamesData struct {
	IdentityCount            int
	Names                    []accountName
	SelectedNameID           *int
	DistinctNameCount        int
	NameSlots                int
	NameCreateCooldown       bool
	NameCreateTimeLeft       string
	NameChangeDurationString string
	NameChangeDuration       time.Duration
	HasPendingNames          bool
}

func (and accountNamesData) getLatestNameCreationTime() time.Time {
	ret := time.Unix(0, 0)
	for _, v := range and.Names {
		if v.TimeCreated.After(ret) && v.Status != "denied" {
			ret = v.TimeCreated
		}
	}
	return ret
}

func (and accountNamesData) hasPendingNames() bool {
	hasPending := false
	for _, v := range and.Names {
		if v.Status == "pending" {
			hasPending = true
			break
		}
	}
	return hasPending
}

func (and accountNamesData) allowedToCreateName() bool {
	hasPending := and.HasPendingNames
	lastNameCreationTime := and.getLatestNameCreationTime()
	nameChangeDuration := getNameChangeDuration()
	return time.Since(lastNameCreationTime) > nameChangeDuration && !hasPending && and.IdentityCount > 0
}

func (and accountNamesData) distinctNameCount() int {
	u := map[string]int{}
	for _, v := range and.Names {
		if v.Status == "denied" {
			continue
		}
		u[v.ClearName] = 1
	}
	return len(u)
}

func (and accountNamesData) allowedToCreateClearName() bool {
	if !and.allowedToCreateName() {
		return false
	}
	return and.distinctNameCount() < and.NameSlots
}

func (and accountNamesData) hasClearName(clearName string) bool {
	for _, v := range and.Names {
		if v.ClearName == clearName {
			return true
		}
	}
	return false
}

func accGetNamesData(ctx context.Context, accountID int) (ret accountNamesData, err error) {
	err = dbpool.QueryRow(ctx, `select name, name_slots from accounts where id = $1`, accountID).Scan(&ret.SelectedNameID, &ret.NameSlots)
	if err != nil {
		return
	}
	err = dbpool.QueryRow(ctx, `select count(*) from identities where account = $1`, accountID).Scan(&ret.IdentityCount)
	if err != nil {
		return
	}
	rows, err := dbpool.Query(ctx, `select id, clear_name, display_name, time_created, status, note from names where account = $1`, accountID)
	if !errors.Is(err, pgx.ErrNoRows) {
		if err != nil {
			return
		}
		ret.Names, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (n accountName, err error) {
			err = row.Scan(&n.ID, &n.ClearName, &n.DisplayName, &n.TimeCreated, &n.Status, &n.Note)
			n.Selected = ret.SelectedNameID != nil && n.ID == *ret.SelectedNameID
			return
		})
	}
	ret.HasPendingNames = ret.hasPendingNames()
	ret.DistinctNameCount = ret.distinctNameCount()
	ret.NameChangeDuration = getNameChangeDuration()
	ret.NameChangeDurationString = ret.NameChangeDuration.String()
	nctl := time.Until(ret.getLatestNameCreationTime().Add(getNameChangeDuration())).Round(time.Minute)
	ret.NameCreateTimeLeft = nctl.String()
	ret.NameCreateCooldown = nctl > 0
	return
}

func getNameChangeDuration() time.Duration {
	return time.Duration(cfg.GetDInt(7, "nameChangeDays")) * (time.Hour * 24)
}
