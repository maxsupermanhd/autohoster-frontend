package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/jackc/pgx/v5"
)

func bansHandler(w http.ResponseWriter, r *http.Request) {
	type viewBan struct {
		ID           int
		Identity     *int
		IdentityName *string
		IdentityKey  *string
		Account      *int
		AccountName  *string
		Reason       string
		IssuedAt     string
		ExpiresAt    string
		IsBanned     bool
		Forbids      string
	}
	ret := []viewBan{}

	var (
		banid           int
		whenbanned      time.Time
		whenexpires     *time.Time
		reason          string
		ident           *int
		identKey        *string
		acc             *int
		accName         *string
		forbidsChatting bool
		forbidsPlaying  bool
		forbidsJoining  bool
	)

	rows, err := dbpool.Query(r.Context(),
		`select
	bans.id, accounts.id, coalesce(names.display_name, substring(coalesce(encode(identities.pkey, 'hex'), identities.hash) for 12)), identities.id, coalesce(encode(identities.pkey, 'hex'), identities.hash),
	time_issued, time_expires, reason, forbids_chatting, forbids_playing, forbids_joining
from bans
left join identities on bans.identity = identities.id
left join accounts on bans.account = accounts.id
left join names on names.id = accounts.name
order by bans.id desc;`)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
			return
		}
	}
	pgx.ForEachRow(rows, []any{&banid, &acc, &accName, &ident, &identKey,
		&whenbanned, &whenexpires, &reason, &forbidsChatting, &forbidsPlaying, &forbidsJoining},
		func() error {
			v := viewBan{
				ID:          banid,
				Identity:    ident,
				IdentityKey: identKey,
				Account:     acc,
				AccountName: accName,
				Reason:      reason,
				IssuedAt:    whenbanned.Format(time.DateTime),
			}
			if whenexpires == nil {
				v.ExpiresAt = "Never"
			} else {
				expiresAt := *whenexpires
				v.ExpiresAt = expiresAt.Format(time.DateTime)
				v.IsBanned = time.Now().Before(expiresAt)
			}
			if forbidsChatting {
				v.Forbids += "chatting"
			}
			if forbidsPlaying {
				v.Forbids += " playing"
			}
			if forbidsJoining {
				v.Forbids += " joining"
			}
			ret = append(ret, v)
			return nil
		})
	if err != nil {
		log.Println(err)
		return
	}
	basicLayoutLookupRespond("bans", w, r, map[string]any{
		"Bans": ret,
	})
}
