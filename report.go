package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/georgysavva/scany/v2/pgxscan"
)

var (
	reportViolations = []string{
		`2. Abuse of the Autohoster, Autorating or any other related system (not listed)`,
		`2.1.1. Disruption or other interference with the system with or without defined purpose.`,
		`2.1.2. Advertizing or propaganda.`,
		`2.1.3. Malicious behavior being either targeted at the system or it's users (i.e. distribution of malware, phishing, attempts at account compromise).`,
		`2.1.4. Use of multiple profiles or accounts per Player.`,
		`2.1.5. Match fixing.`,
		`2.1.6. Impersonation or attempts at impersonation.`,
		`2.1.7. Ban/punishment evasion.`,
		`3. Lobby moderation (not listed)`,
		`3.2.1. Excessive restriction Players participation in Games. (by moderators)`,
		`4. Chat and nicknames (not listed)`,
		`4.1.3. Personal and group insults, gender-based humiliation, sexual orientation, religion, and other topics that are not compatible with generally accepted morality principles and decency. This may include discussion of negative or controversial historical events or other obvious topics that can lead to dissent and insulting contexts.`,
		`4.1.5. Political and religious propaganda.`,
		`4.1.7. Any manifestations of Nazism, nationalism, incitement of interracial, interethnic, interfaith discord and hostility, calls for the overthrow of the government by force.`,
		`4.1.8. Disclosure other players' personal data.`,
		`5. Unsportsmanlike conduct (not listed)`,
		`5.1.1. Actively blocking access to parts of the playfield. (Walling in teammates)`,
		`5.1.2. Swapping Profile before starting the game.`,
		`5.1.3. Getting spectator-level information while participating in a game as a player.`,
		`5.1.4. Providing spectator-level information to a player.`,
	}
)

func reportAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !checkUserAuthorized(r) {
		respondWithUnauthorized(w, r)
		return false
	}
	profileCount := 0
	var lastreport time.Time
	err := dbpool.QueryRow(r.Context(), `SELECT a.last_report, (SELECT count(*) FROM identities WHERE account = a.id) FROM accounts as a WHERE username = $1`, sessionGetUsername(r)).Scan(&lastreport, &profileCount)
	if DBErr(w, r, err) {
		return false
	}
	if profileCount == 0 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You must link in-game profile first to be able to report others"})
		return false
	}
	if time.Since(lastreport).Hours() < 2 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You can submit only one report in 2 hours (administration will investigate for all violations, do not report single player multiple times)"})
		return false
	}
	return true
}

func reportHandlerGET(w http.ResponseWriter, r *http.Request) {
	var reports []struct {
		ID           int
		Whenreported time.Time
		Resolution   *string
	}
	err := pgxscan.Select(r.Context(), dbpool, &reports, `select id, whenreported, resolution from reports where reporter = $1 order by whenreported desc`, sessionGetUsername(r))
	if DBErr(w, r, err) {
		return
	}
	basicLayoutLookupRespond("report", w, r, map[string]any{
		"reasons":             reportViolations,
		"datetimeNow":         time.Now().Format("2006-01-02T15:04"),
		"datetimeMinus30Days": time.Now().Add(-30 * 24 * time.Hour).Format("2006-01-02T15:04"),
		"reports":             reports,
	})
}

func reportValidateOffender(v string) bool {
	found := false
	err := dbpool.QueryRow(context.Background(), `select count(*) > 0 from identities where hash = $1 or encode(pkey, 'base64') = $1 or encode(pkey, 'hex') = $1`, v).Scan(&found)
	if err != nil {
		log.Println("error checking report target validity on identity: ", err.Error())
		return false
	}
	if found {
		return true
	}
	err = dbpool.QueryRow(context.Background(), `select count(*) > 0 from names where clear_name = $1 or display_name = $1`, v).Scan(&found)
	if err != nil {
		log.Println("error checking report target validity on names: ", err.Error())
		return false
	}
	if found {
		return true
	}
	return false
}

func reportHandlerValidatePlayerInput(w http.ResponseWriter, r *http.Request) {
	off := r.FormValue("offender")
	basicLayoutLookupRespond("reportPlayerSearch", w, r, map[string]any{
		"valid": reportValidateOffender(off),
		"value": off,
	})
}

func reportHandlerPOST(w http.ResponseWriter, r *http.Request) {
	if !reportAllowed(w, r) {
		return
	}

	err := r.ParseForm()
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Invalid form"})
		return
	}

	if r.FormValue("agree1") != "on" || r.FormValue("agree2") != "on" || r.FormValue("agree3") != "on" || r.FormValue("agree4") != "on" {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "You must understand reporting rules"})
		return
	}

	iViolation := r.FormValue("violation")
	iViolationTime := r.FormValue("violationTime")
	iOffender := r.FormValue("offender")
	iComment := r.FormValue("comment")

	if !slices.Contains(reportViolations, iViolation) {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Violation must be selected from the list"})
		return
	}

	iViolationTimeParsed, err := time.Parse("2006-01-02T15:04", iViolationTime)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Incorrect violation time format"})
		return
	}
	iViolationTimeSince := time.Since(iViolationTimeParsed)
	if iViolationTimeSince > 30*24*time.Hour {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Violations older than 30 days are not investigated"})
		return
	}
	if iViolationTimeSince < 0 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Violations from the future will have to cross current time border before being reported"})
		return
	}

	if !reportValidateOffender(iOffender) {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Offender was not found in the Autohoster system"})
		return
	}

	if len(iComment) > 1500 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Comment is too long"})
		return
	}

	_, err = dbpool.Exec(r.Context(), `INSERT INTO reports (reporter, violation, violationtime, offender, comment) VALUES ($1, $2, $3, $4, $5)`,
		sessionGetUsername(r), iViolation, iViolationTime, iOffender, iComment)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Error occured, contact administrator"})
		log.Println(err)
		return
	}

	_, err = dbpool.Exec(r.Context(), `UPDATE accounts SET last_report = now() WHERE username = $1`, sessionGetUsername(r))
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Error occured, contact administrator"})
		log.Println(err)
		return
	}

	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Report successfully submitted."})
	sendReportWebhook(fmt.Sprintf("User `%s` reported violations `%s` of a player `%s` at `%s`",
		escapeBacktick(sessionGetUsername(r)),
		escapeBacktick(r.FormValue("violation")),
		escapeBacktick(r.FormValue("offender")),
		escapeBacktick(r.FormValue("violationTime"))))
}

func sendReportWebhook(content string) error {
	return sendWebhook(cfg.GetDSString("", "webhooks", "reports"), content)
}
