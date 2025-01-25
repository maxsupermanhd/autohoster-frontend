package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
)

func modSendWebhook(content string) error {
	return sendWebhook(cfg.GetDSString("", "webhooks", "actions"), content)
}

func isSuperadmin(context context.Context, username string) bool {
	ret := false
	derr := dbpool.QueryRow(context, "SELECT superadmin FROM accounts WHERE username = $1", username).Scan(&ret)
	if derr != nil {
		if errors.Is(derr, pgx.ErrNoRows) {
			return false
		}
		log.Printf("Error checking superadmin: %v", derr)
	}
	return ret
}

func basicSuperadminHandler(page string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isSuperadmin(r.Context(), sessionGetUsername(r)) {
			respondWithForbidden(w, r)
			return
		}
		basicLayoutLookupRespond(page, w, r, nil)
	}
}

func SuperadminCheck(next func(w http.ResponseWriter, r *http.Request)) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isSuperadmin(r.Context(), sessionGetUsername(r)) {
			respondWithForbidden(w, r)
			return
		}
		next(w, r)
	}
}

func APISuperadminCheck(next func(w http.ResponseWriter, r *http.Request) (int, any)) func(w http.ResponseWriter, r *http.Request) (int, any) {
	return func(w http.ResponseWriter, r *http.Request) (int, any) {
		if !isSuperadmin(r.Context(), sessionGetUsername(r)) {
			return http.StatusForbidden, nil
		}
		return next(w, r)
	}
}

func APIgetAccounts2(_ http.ResponseWriter, r *http.Request) (int, any) {
	return genericViewRequest[struct {
		ID               int        `json:"id"`
		Username         string     `json:"username"`
		Email            string     `json:"email"`
		AccountCreated   time.Time  `json:"account_created"`
		LastSeen         *time.Time `json:"last_seen"`
		EmailConfirmed   *time.Time `json:"email_confirmed"`
		Terminated       bool       `json:"terminated"`
		AllowHostRequest bool       `json:"allow_host_request"`
		DisplayName      *string    `json:"display_name"`
		LastReport       *time.Time `json:"last_report"`
		LastRequest      *time.Time `json:"last_request"`
		Identities       string     `json:"identities"`
	}](r, genericRequestParams{
		tableName:               "accounts_view",
		limitClamp:              1500,
		sortDefaultOrder:        "desc",
		sortDefaultColumn:       "id",
		sortColumns:             []string{"id", "account_created"},
		filterColumnsFull:       []string{"id"},
		filterColumnsStartsWith: []string{"username", "email", "display_name"},
		searchColumn:            "username || email || display_name",
		searchSimilarity:        0.3,
		columnMappings: map[string]string{
			"id":              "id",
			"username":        "username",
			"email":           "email",
			"account_created": "account_created",
			"last_seen":       "last_seen",
			"email_confirmed": "email_confirmed",
			"terminated":      "terminated",
			"last_report":     "last_report",
			"last_request":    "last_request",
		},
	})
}

func APIresendEmailConfirm(_ http.ResponseWriter, r *http.Request) (int, any) {
	params := mux.Vars(r)
	id, err := strconv.Atoi(params["id"])
	if err != nil {
		return 400, nil
	}
	modSendWebhook(fmt.Sprintf("Administrator `%s` resent activation email for account `%v`", sessionGetUsername(r), id))
	return 200, modResendEmailConfirm(id)
}

func modAccountsPOST(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(4096)
	if err != nil {
		respondWithCodeAndPlaintext(w, 400, "Failed to parse form")
		return
	}
	if !stringOneOf(r.FormValue("param"), "bypass_ispban", "allow_host_request", "terminated", "no_request_reason") {
		respondWithCodeAndPlaintext(w, 400, "Param is bad ("+r.FormValue("param")+")")
		return
	}
	if stringOneOf(r.FormValue("param"), "bypass_ispban", "allow_host_request", "terminated") {
		if !stringOneOf(r.FormValue("val"), "true", "false") {
			respondWithCodeAndPlaintext(w, 400, "Val is bad")
			return
		}
	}
	if r.FormValue("name") == "" {
		respondWithCodeAndPlaintext(w, 400, "Name is missing")
		return
	}
	tag, err := dbpool.Exec(context.Background(), "UPDATE accounts SET "+r.FormValue("param")+" = $1 WHERE username = $2", r.FormValue("val"), r.FormValue("name"))
	if err != nil {
		logRespondWithCodeAndPlaintext(w, 500, "Database query error: "+err.Error())
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	if !tag.Update() || tag.RowsAffected() != 1 {
		logRespondWithCodeAndPlaintext(w, 500, "Sus result "+tag.String())
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", tag.String(), string(debug.Stack())))
		return
	}
	w.WriteHeader(200)
	err = modSendWebhook(fmt.Sprintf("Administrator `%s` changed `%s` to `%s` for user `%s`.", sessionGetUsername(r), r.FormValue("param"), r.FormValue("val"), r.FormValue("name")))
	if err != nil {
		log.Println(err)
	}
	if r.FormValue("param") == "norequest_reason" {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msggreen": true, "msg": "Success"})
		w.Header().Set("Refresh", "1; /moderation/accounts")
	}
}

func modNewsPOST(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		respondWithCodeAndPlaintext(w, 400, "Failed to parse form: "+err.Error())
		return
	}
	tag, err := dbpool.Exec(r.Context(), `insert into announcements (title, content, color, when_posted) values ($1, $2, $3, $4)`, r.FormValue("title"), r.FormValue("content"), r.FormValue("color"), r.FormValue("date"))
	result := ""
	if err != nil {
		result = err.Error()
	} else {
		result = tag.String()
	}
	msg := template.HTML(result + `<br><a href="/moderation/news">back</a>`)
	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"nocenter": true, "plaintext": true, "msg": msg})
}

func modBansPOST(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		respondWithCodeAndPlaintext(w, 400, "Failed to parse form: "+err.Error())
		return
	}
	dur := parseFormInt(r, "duration")
	var inExpires *time.Time
	if dur != nil && *dur != 0 {
		d := time.Now().Add(time.Duration(*dur) * time.Second)
		inExpires = &d
	}
	inAccount := parseFormInt(r, "account")
	inIdentity := parseFormInt(r, "identity")
	if inAccount == nil && inIdentity == nil {
		respondWithCodeAndPlaintext(w, 400, "Both identity and account are nil")
		return
	}

	inForbidsJoining := parseFormBool(r, "forbids-joining")
	inForbidsChatting := parseFormBool(r, "forbids-chatting")
	inForbidsPlaying := parseFormBool(r, "forbids-playing")

	tag, err := dbpool.Exec(r.Context(),
		`insert into bans
(account, identity, time_expires, reason, forbids_joining, forbids_chatting, forbids_playing) values
($1, $2, $3, $4, $5, $6, $7)`, inAccount, inIdentity, inExpires, r.FormValue("reason"),
		inForbidsJoining, inForbidsChatting, inForbidsPlaying)
	result := ""
	if err != nil {
		result = err.Error()
	} else {
		result = tag.String()
	}
	msg := template.HTML(result + `<br><a href="/moderation/bans">back</a>`)
	modSendWebhook(fmt.Sprintf("Administrator `%s` banned"+
		"\naccount `%+#v` identity `%+#v`"+
		"\nfor `%+#v` (ends at `%+#v`)"+
		"\nduration `%+#v`"+
		"\njoining `%+#v` `%+#v`"+
		"\nchatting `%+#v` `%+#v`"+
		"\nplaying `%+#v` `%+#v`",
		sessionGetUsername(r),
		r.FormValue("account"), r.FormValue("identity"),
		r.FormValue("reason"), dur, inExpires,
		r.FormValue("forbids-joining"), inForbidsJoining,
		r.FormValue("forbids-chatting"), inForbidsChatting,
		r.FormValue("forbids-playing"), inForbidsPlaying))
	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"nocenter": true, "plaintext": true, "msg": msg})
}

func APIgetBans(_ http.ResponseWriter, r *http.Request) (int, any) {
	var ret []byte
	derr := dbpool.QueryRow(r.Context(), `SELECT array_to_json(array_agg(to_json(bans))) FROM bans;`).Scan(&ret)
	if derr != nil {
		return 500, derr
	}
	return 200, ret
}

func APIgetLogs2(_ http.ResponseWriter, r *http.Request) (int, any) {
	return genericViewRequest[struct {
		ID       int       `json:"id"`
		Whensent time.Time `json:"whensent"`
		Pkey     string    `json:"pkey"`
		Name     string    `json:"name"`
		Msgtype  *string   `json:"msgtype"`
		Msg      string    `json:"msg"`
	}](r, genericRequestParams{
		tableName:               "composelog",
		limitClamp:              1500,
		sortDefaultOrder:        "desc",
		sortDefaultColumn:       "whensent",
		sortColumns:             []string{"id", "whensent"},
		filterColumnsFull:       []string{"id", "msg"},
		filterColumnsStartsWith: []string{"name", "pkey", "msgtype"},
		searchColumn:            "name || msg",
		searchSimilarity:        0.3,
		columnMappings: map[string]string{
			"id":       "id",
			"whensent": "whensent",
			"pkey":     "pkey",
			"name":     "name",
			"msgtype":  "msgtype",
			"msg":      "msg",
		},
	})
}

func APIgetIdentities(_ http.ResponseWriter, r *http.Request) (int, any) {
	return genericViewRequest[struct {
		ID      int
		Name    string
		Pkey    []byte
		Hash    string
		Account *int
	}](r, genericRequestParams{
		tableName:               "identities_view",
		limitClamp:              500,
		sortDefaultOrder:        "desc",
		sortDefaultColumn:       "id",
		sortColumns:             []string{"id", "name", "account"},
		filterColumnsFull:       []string{"id", "account"},
		filterColumnsStartsWith: []string{"name", "pkey", "hash"},
		searchColumn:            "name",
		searchSimilarity:        0.3,
		columnMappings: map[string]string{
			"ID":      "id",
			"Name":    "name",
			"Pkey":    "pkey",
			"Hash":    "hash",
			"Account": "account",
		},
	})
}

func APIgetNames(_ http.ResponseWriter, r *http.Request) (int, any) {
	return genericViewRequest[struct {
		ID          int
		Account     int
		ClearName   string
		DisplayName string
		TimeCreated time.Time
		Status      string
		Note        string
	}](r, genericRequestParams{
		tableName:               "names",
		limitClamp:              500,
		sortDefaultOrder:        "desc",
		sortDefaultColumn:       "id",
		sortColumns:             []string{"id", "clear_name", "display_name", "status", "account"},
		filterColumnsFull:       []string{"id", "account"},
		filterColumnsStartsWith: []string{"clear_name", "display_name"},
		searchColumn:            "clear_name",
		searchSimilarity:        0.1,
		columnMappings: map[string]string{
			"ID":          "id",
			"Account":     "account",
			"ClearName":   "clear_name",
			"DisplayName": "display_name",
			"TimeCreated": "time_created",
			"Status":      "status",
			"Note":        "note",
		},
	})
}

func modNamesHandler(w http.ResponseWriter, r *http.Request) {
	status := r.FormValue("status")
	nameID := r.FormValue("nameID")
	note := r.FormValue("note")
	if !stringOneOf(status, "approved", "denied") {
		respondWithCodeAndPlaintext(w, 400, "Param is bad ("+status+")")
		return
	}
	rows, err := dbpool.Query(context.Background(), `update names set status = $1, note = $2 where id = $3 returning clear_name, display_name`, status, note, nameID)
	if DBErr(w, r, err) {
		return
	}
	tag := rows.CommandTag()
	rets, err := pgx.CollectOneRow(rows, func(row pgx.CollectableRow) (struct {
		clearName   string
		displayName string
	}, error) {
		ret := struct {
			clearName   string
			displayName string
		}{}
		err := row.Scan(&ret.clearName, &ret.displayName)
		return ret, err
	})
	if DBErr(w, r, err) {
		return
	}
	if !tag.Update() || tag.RowsAffected() != 1 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", tag.String(), string(debug.Stack())))
		return
	}
	modSendWebhook(fmt.Sprintf("Administrator `%s` `%s` name `%s` `%s` (note `%s`)", sessionGetUsername(r), status, rets.clearName, rets.displayName, note))
	basicLayoutLookupRespond("modNames", w, r, nil)
}

func modResendEmailConfirm(accountID int) error {
	var email, emailcode string
	err := dbpool.QueryRow(context.Background(), `SELECT email, email_confirm_code FROM accounts WHERE id = $1`, accountID).Scan(&email, &emailcode)
	if err != nil {
		if err == pgx.ErrNoRows {
			return errors.New("no account")
		}
		return err
	}
	return sendgridConfirmcode(email, emailcode)
}

func modReloadConfig(w http.ResponseWriter, r *http.Request) {
	err := cfg.SetFromFileJSON("config.json")
	w.WriteHeader(200)
	w.Write([]byte(fmt.Sprintf("%v\n\n", err)))
}

func APImodInstances(w http.ResponseWriter, r *http.Request) (int, any) {
	cl := http.Client{Timeout: 2 * time.Second}
	h, ok := cfg.GetString("backend", "urlBase")
	if !ok {
		return 500, "backend url base not set"
	}
	rsp, err := cl.Get(h + "instances")
	if err != nil {
		return 500, err
	}
	rspbb, err := io.ReadAll(rsp.Body)
	if err != nil {
		return 500, err
	}
	i := map[string]map[string]any{}
	err = json.Unmarshal(rspbb, &i)
	if err != nil {
		return 500, err
	}
	ii := []map[string]any{}
	for k, v := range i {
		v["ID"] = k
		ii = append(ii, v)
	}
	return 200, ii
}

func modDebugInstanceToGame(w http.ResponseWriter, r *http.Request) {
	inputtedInstanceIDString := r.URL.Query().Get("instID")
	inst, err := strconv.Atoi(inputtedInstanceIDString)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": err.Error()})
		return
	}
	var gameID *time.Time
	err = dbpool.QueryRow(r.Context(), `select time_started from games where instance = $1`, inst).Scan(&gameID)
	if errors.Is(err, pgx.ErrNoRows) {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "game of that instance not found"})
		return
	}
	if DBErr(w, r, err) {
		return
	}
	gameIDBytes, err := gameID.MarshalText()
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": err.Error()})
		return
	}
	w.Header().Add("Location", "/games/"+string(gameIDBytes))
	w.WriteHeader(http.StatusTemporaryRedirect)
}
