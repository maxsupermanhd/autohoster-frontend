package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/pgxpool"
	mapsdatabase "github.com/maxsupermanhd/go-wz/maps-database"
)

var regexMaphash = regexp.MustCompile(`^[a-zA-Z0-9-]*$`)

func hostRequestHandlerPOST(w http.ResponseWriter, r *http.Request) {
	if !hostRequestAccountPassesChecks(w, r) {
		return
	}
	err := r.ParseForm()
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Failed to parse from"})
		return
	}

	roomName := parseFormString(r, "roomName", nil)
	if roomName == nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Invalid roomName"})
		return
	}
	mapHash := parseFormString(r, "mapHash", regexMaphash)
	if mapHash == nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Invalid mapHash"})
		return
	}
	timeLimit := 90
	if v := parseFormInt(r, "timeLimit"); v != nil {
		timeLimit = *v
		if timeLimit < 15 {
			timeLimit = 15
		}
		if timeLimit > 60*3 {
			timeLimit = 60 * 3
		}
	}
	settingsAlliances := 2
	if v := parseFormIntWhitelist(r, "settingsAlliances", 0, 1, 2, 3); v != nil {
		settingsAlliances = *v
	}
	settingsScav := 0
	if v := parseFormIntWhitelist(r, "settingsScav", 0, 1); v != nil {
		settingsScav = *v
	}
	settingsBase := 2
	if v := parseFormIntWhitelist(r, "settingsBase", 1, 2, 3); v != nil {
		settingsBase = *v
	}

	inf, err := mapsdatabase.FetchMapInfo(*mapHash)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Failed to fetch map info: " + err.Error()})
		return
	}
	if !inf.Player.Units.Eq ||
		!inf.Player.Structs.Eq ||
		!inf.Player.ResourceExtr.Eq ||
		!inf.Player.PwrGen.Eq ||
		!inf.Player.RegFact.Eq ||
		!inf.Player.VtolFact.Eq ||
		!inf.Player.CyborgFact.Eq ||
		!inf.Player.ResearchCent.Eq ||
		!inf.Player.DefStruct.Eq {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Provided map does not meet balance requirements"})
		return
	}

	userAdminFound := false
	for _, v := range r.Form["additionalAdmin"] {
		adminId, err := strconv.Atoi(v)
		if err != nil {
			continue
		}
		if sessionGetUserID(r) == adminId {
			userAdminFound = true
			break
		}
	}
	if !userAdminFound {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Map requester must be an admin"})
		return
	}

	var adminHashes []string
	err = dbpool.QueryRow(r.Context(),
		`select
	coalesce(array_agg(encode(sha256(i.pkey), 'hex')), '{}'::text[])
from accounts as a
join identities as i on i.account = a.id
where (a.id = any($1) or a.superadmin = true) and i.pkey is not null;`, r.Form["additionalAdmin"]).Scan(&adminHashes)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}

	ratingCategories := []int{}
	switch r.Form.Get("ratingCategories") {
	case "ratingNoCategories":
		ratingCategories = []int{}
	case "ratingRegular":
		whitelistedMaps, ok := cfg.GetMapStringAny("whitelistedMaps")
		if !ok {
			whitelistedMaps = map[string]any{}
		}
		isWhitelisted := false
		for _, v := range whitelistedMaps {
			switch vv := v.(type) {
			case map[string]any:
				h, ok := vv["Hash"].(string)
				if !ok {
					continue
				}
				if h == inf.Download.Hash {
					isWhitelisted = true
					break
				}
			}
		}
		if !isWhitelisted {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Map is not whitelisted for rating"})
			return
		}
		ratingCategories = []int{3}
	}

	var account_clear_name *string
	err = dbpool.QueryRow(r.Context(), `select n.display_name
from accounts as a
join names as n on n.id = a.name
where a.id = $1`, sessionGetUserID(r)).Scan(&account_clear_name)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	if account_clear_name == nil || (account_clear_name != nil && *account_clear_name == "") {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You must have name registered to request rooms"})
		return
	}

	toSendPreset := map[string]any{
		"adminsPolicy":       "whitelist",
		"admins":             adminHashes,
		"allowNonLinkedJoin": parseFormBool(r, "allowNonRegisteredJoin"),
		"allowNonLinkedPlay": parseFormBool(r, "allowNonRegisteredPlay"),
		"allowNonLinkedChat": parseFormBool(r, "allowNonRegisteredChat"),
		"timelimit":          timeLimit,
		"displayCategory":    3,
		"ratingCategories":   ratingCategories,
		"players":            inf.Slots,
		"roomName":           roomName,
		"settingsBase":       strconv.Itoa(settingsBase),
		"settingsPower":      "2",
		"settingsAlliance":   strconv.Itoa(settingsAlliances),
		"settingsScavs":      strconv.Itoa(settingsScav),
		"maps": map[string]any{
			inf.Name: map[string]any{
				"hash": inf.Download.Hash,
			},
		},
		"frameinterval": inf.Slots / 2,
		"motds": map[string]any{
			"9 requested": "This game was requested by " + *account_clear_name,
		},
	}
	spew.Dump(toSendPreset)

	hosterResponse, err := RequestHosting(toSendPreset)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}

	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Success, hoster responded: " + hosterResponse})

}

func hostRequestHandlerGET(w http.ResponseWriter, r *http.Request) {
	if !hostRequestAccountPassesChecks(w, r) {
		return
	}
	s, _ := RequestStatus()
	if !s {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Autohoster backend unavaliable"})
		return
	}
	admins := []*struct {
		DisplayName string `db:"display_name"`
		ID          int
	}{}
	err := pgxscan.Select(r.Context(), dbpool, &admins, `select distinct on (a.id) n.display_name, a.id
from accounts as a
join identities as i on i.account = a.id
join names as n on n.id = a.name
where a.allow_host_request = true and i.pkey is not null
order by a.id`)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	whitelistedMaps, ok := cfg.GetMapStringAny("whitelistedMaps")
	if !ok {
		whitelistedMaps = map[string]any{"not": "set"}
	}
	basicLayoutLookupRespond("hostrequest", w, r, map[string]any{
		"Admins":          admins,
		"WhitelistedMaps": whitelistedMaps,
	})
}

func hostRequestAccountPassesChecks(w http.ResponseWriter, r *http.Request) bool {
	if !checkUserAuthorized(r) {
		basicLayoutLookupRespond("noauth", w, r, map[string]any{})
		return false
	}
	identCount := 0
	err := dbpool.QueryRow(r.Context(), `select count(pkey) from identities where account = $1 and pkey is not null`, sessionGetUserID(r)).Scan(&identCount)
	if err != nil {
		if err == pgx.ErrNoRows {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Unauthorized?!"})
			sessionManager.Destroy(r.Context())
		} else {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		}
		return false
	}
	if identCount < 1 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You must have at least one linked identity with known public key"})
		return false
	}
	var allow_host_request bool
	var no_request_reason string
	var last_request time.Time
	err = dbpool.QueryRow(r.Context(), `SELECT allow_host_request, no_request_reason, last_request FROM accounts WHERE username = $1`,
		sessionGetUsername(r)).Scan(&allow_host_request, &no_request_reason, &last_request)
	if err != nil {
		if err == pgx.ErrNoRows {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Unauthorized?!"})
			sessionManager.Destroy(r.Context())
		} else {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		}
		return false
	}
	if !allow_host_request {
		basicLayoutLookupRespond("errornorequest", w, r, map[string]any{"ForbiddenReason": no_request_reason})
		return false
	}
	if time.Since(last_request) < 5*time.Minute {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You can only request one room every so often, please wait before opening next one"})
		return false
	}
	return true
}

func wzlinkCheckHandler(w http.ResponseWriter, r *http.Request) {
	if !checkUserAuthorized(r) {
		basicLayoutLookupRespond("noauth", w, r, map[string]any{})
		return
	}
	var confirmcode string
	err := dbpool.QueryRow(r.Context(), `SELECT coalesce(wz_confirm_code, '') FROM accounts WHERE username = $1`, sessionGetUsername(r)).Scan(&confirmcode)
	if err != nil {
		if err == pgx.ErrNoRows {
			sessionManager.Destroy(r.Context())
			w.Header().Set("Refresh", "1; /")
			w.WriteHeader(http.StatusConflict)
			return
		}
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	if confirmcode == "" {
		confirmcode = "confirm-" + generateRandomString(18)
		_, err := dbpool.Exec(r.Context(), `update accounts set wz_confirm_code = $1 where username = $2`, confirmcode, sessionGetUsername(r))
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
			return
		}
		basicLayoutLookupRespond("wzlinkcheck", w, r, map[string]any{"LinkStatus": "code", "WzConfirmCode": "/hostmsg " + confirmcode})
		return
	}
	basicLayoutLookupRespond("wzlinkcheck", w, r, map[string]any{"LinkStatus": "code", "WzConfirmCode": "/hostmsg " + confirmcode})
}

func wzlinkHandler(w http.ResponseWriter, r *http.Request) {
	if !checkUserAuthorized(r) {
		basicLayoutLookupRespond("noauth", w, r, map[string]any{})
		return
	}
	idt := []struct {
		ID      int
		Name    string
		Pkey    []byte
		Hash    string
		Account int
	}{}
	err := pgxscan.Select(r.Context(), dbpool, &idt, `select id, name, pkey, hash, account from identities where account = $1`, sessionGetUserID(r))
	if errors.Is(err, pgx.ErrNoRows) {
		basicLayoutLookupRespond("wzlink", w, r, map[string]any{
			"Identities": idt,
		})
		return
	}
	if DBErr(w, r, err) {
		return
	}
	basicLayoutLookupRespond("wzlink", w, r, map[string]any{
		"Identities": idt,
	})
}

func RequestStatus() (bool, string) {
	req, err := http.NewRequest("GET", cfg.GetDSString("http://localhost:9271/", "backendUrl")+"alive", nil)
	if err != nil {
		log.Print(err)
		return false, err.Error()
	}
	var netClient = &http.Client{
		Timeout: time.Second * 2,
	}
	resp, err := netClient.Do(req)
	if err != nil {
		log.Print(err)
		return false, err.Error()
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Print(err)
		return false, err.Error()
	}
	bodyString := string(bodyBytes) + "\n"
	return true, bodyString
}

func RequestHosting(preset map[string]any) (string, error) {
	reqBodyBytes, err := json.Marshal(preset)
	if err != nil {
		return "", err
	}
	reqBodyBuf := bytes.NewBuffer(reqBodyBytes)
	req, err := http.NewRequest("POST", cfg.GetDSString("http://localhost:9271/", "backendUrl")+"request", reqBodyBuf)
	if err != nil {
		return "", err
	}
	var netClient = &http.Client{
		Timeout: time.Second * 2,
	}
	resp, err := netClient.Do(req)
	if err != nil {
		return "", err
	}
	rspBodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(rspBodyBytes) + "\n", nil
}
