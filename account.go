package main

import (
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"runtime/debug"

	"github.com/jackc/pgx/v5"
)

const (
	templateLogin             = "login"
	templateLoginFormUsername = "username"
	templateLoginFormPassword = "password"
)

func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !checkFormParse(w, r) {
			return
		}
		log.Printf("Login attempt: [%s]", r.PostFormValue(templateLoginFormUsername))
		if !validatePassword(r.PostFormValue(templateLoginFormPassword)) {
			basicLayoutLookupRespond(templateLogin, w, r, map[string]any{"LoginError": true})
			return
		}
		var passdb string
		var iddb int
		var terminated bool
		var username string
		err := dbpool.QueryRow(r.Context(), "SELECT username, password, id, terminated FROM accounts WHERE username = $1 or email = $1", r.PostFormValue("username")).Scan(&username, &passdb, &iddb, &terminated)
		if err != nil {
			if err == pgx.ErrNoRows {
				basicLayoutLookupRespond(templateLogin, w, r, map[string]any{"LoginError": true})
				log.Printf("No such user")
			} else {
				basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
				notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
			}
			return
		}
		if comparePasswords(passdb, r.PostFormValue("password")) {
			if terminated {
				basicLayoutLookupRespond(templateLogin, w, r, map[string]any{"LoginError": true,
					"LoginDetailedError": template.HTML("<p><b>Account was terminated.</b></p><p><a href=\"/about#contact\">Contact administrator</a> for further information.</p><p>Creating more accounts will not help and will only get you permanently banned.</p>")})
				log.Printf("Terminated account [%s] success login attempt", username)
				return
			}
			sessionManager.Put(r.Context(), "User.Username", username)
			sessionManager.Put(r.Context(), "User.Id", iddb)
			sessionManager.Put(r.Context(), "UserAuthorized", "True")
			w.Header().Set("Refresh", "1; /account")
			basicLayoutLookupRespond(templateLogin, w, r, map[string]any{"LoginComplete": true, "User": map[string]any{"Username": username}})
			log.Printf("Log in success: [%s]", username)
		} else {
			basicLayoutLookupRespond("login", w, r, map[string]any{"LoginError": true})
		}
	} else {
		if r.Header.Get("CF-Visitor") != "{\"scheme\":\"https\"}" {
			basicLayoutLookupRespond(templateLogin, w, r, map[string]any{"WarningUnsafe": true})
		} else {
			basicLayoutLookupRespond(templateLogin, w, r, map[string]any{})
		}
	}
}

func terminatedHandler(w http.ResponseWriter, r *http.Request) {
	sessionManager.Destroy(r.Context())
	w.Header().Set("Refresh", "2; /login")
	w.Header().Set("Clear-Site-Data", `"cache", "cookies", "storage", "executionContexts"`)
	basicLayoutLookupRespond(templateLogin, w, r, map[string]any{"LoginError": true,
		"LoginDetailedError": template.HTML("<p><b>Account was terminated.</b></p><p><a href=\"/about#contact\">Contact administrator</a> for further information.</p><p>Creating more accounts will not help and will only get you permanently banned.</p>")})
}

func logoutHandler(w http.ResponseWriter, r *http.Request) {
	sessionManager.Destroy(r.Context())
	w.Header().Set("Refresh", "2; /login")
	w.Header().Set("Clear-Site-Data", `"cache", "cookies", "storage", "executionContexts"`)
	basicLayoutLookupRespond("logout", w, r, map[string]any{})
}

func checkIPMatchesConfig(ip string, confpath ...string) bool {
	clip := net.ParseIP(ip)
	if clip == nil {
		return false
	}
	ipmatchs := map[string]bool{}
	o, ok := cfg.GetKeys(confpath...)
	if !ok {
		return false
	}
	for _, k := range o {
		s, ok := cfg.GetBool(append(confpath, k)...)
		if !ok {
			return false
		}
		if !s {
			delete(ipmatchs, k)
		} else {
			ipmatchs[k] = s
		}
	}
	for kip, v := range ipmatchs {
		if !v {
			continue
		}
		_, pnt, err := net.ParseCIDR(kip)
		if err != nil {
			log.Printf("ipmatch ip %q is not in CIDR notation: %s", kip, err)
			continue
		}
		if pnt == nil {
			log.Printf("ipmatch ip %q has no network after parsing", kip)
			continue
		}
		if pnt.Contains(clip) {
			log.Printf("ipmatch applied to client %q with rule %q", ip, kip)
			return true
		}
	}
	return false
}

func registerHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.Header.Get("CF-Connecting-IP")
	if ip == "" {
		ip = r.RemoteAddr
	}
	if checkIPMatchesConfig(ip, "ipnoreg") {
		basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "You are not allowed to create accounts. Contact administration."})
		return
	}

	if r.Method == http.MethodPost {
		err := r.ParseForm()
		if err != nil {
			log.Printf("Error reading form: %v\n", err)
			return
		}
		type LastAttemptS struct {
			Username string
			Password string
			Email    string
		}
		la := LastAttemptS{r.PostFormValue("username"), r.PostFormValue("password"), r.PostFormValue("email")}
		if !validateUsername(r.PostFormValue("username")) {
			basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "Username length must be between 3 and 25 and not contain '@' character", "LastAttempt": la})
			return
		}
		if !validatePassword(r.PostFormValue("password")) {
			basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "Password length must be between 6 and 25", "LastAttempt": la})
			return
		}
		if r.PostFormValue("password") != r.PostFormValue("confirm-password") {
			basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "Passwords are not matching", "LastAttempt": la})
			return
		}
		if !validateEmail(r.PostFormValue("email")) {
			basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "Email is not valid", "LastAttempt": la})
			return
		}
		requname := r.PostFormValue("username")
		requpass := hashPassword(r.PostFormValue("password"))
		reqemail := r.PostFormValue("email")
		reqemailcode := generateRandomString(50)
		dbRegisterLock.Lock()
		defer dbRegisterLock.Unlock()

		log.Printf("Register attempt: [%s] [%s]", requname, reqemail)

		numUsername := 0
		err = dbpool.QueryRow(r.Context(), "SELECT COUNT(*) FROM accounts WHERE lower(username) = lower($1)", requname).Scan(&numUsername)
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
			return
		}
		if numUsername != 0 {
			basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "Username is already taken! (case insensitive)", "LastAttempt": la})
			return
		}

		numEmail := 0
		err = dbpool.QueryRow(r.Context(), "SELECT COUNT(*) FROM accounts WHERE lower(email) = lower($1)", reqemail).Scan(&numEmail)
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
			return
		}
		if numEmail != 0 {
			basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "Email is already taken!", "LastAttempt": la})
			return
		}

		if err := sendgridConfirmcode(reqemail, reqemailcode); err != nil {
			log.Println("Failed to send email: ", err)
			basicLayoutLookupRespond("register", w, r, map[string]any{"RegisterErrorMsg": "Can't verify email. Contact administrator.", "LastAttempt": la})
			return
		}

		tag, err := dbpool.Exec(r.Context(), "INSERT INTO accounts (username, password, email, email_confirm_code) VALUES($1, $2, $3, $4) ON CONFLICT DO NOTHING", requname, requpass, reqemail, reqemailcode)
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
			return
		}
		if tag.RowsAffected() != 1 {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", tag.String(), string(debug.Stack())))
			return
		}
		basicLayoutLookupRespond("register", w, r, map[string]any{"SuccessRegister": true})
		log.Printf("Register attempt success: [%s] [%s]", requname, reqemail)
		sendWebhook(cfg.GetDSString("", "webhooks", "actions"), fmt.Sprintf(`New account registered %q`, requname))
	} else {
		if r.Header.Get("CF-Visitor") != "{\"scheme\":\"https\"}" {
			basicLayoutLookupRespond("register", w, r, map[string]any{"WarningUnsafe": true})
		} else {
			basicLayoutLookupRespond("register", w, r, map[string]any{})
		}
	}
}

func emailconfHandler(w http.ResponseWriter, r *http.Request) {
	keys, ok := r.URL.Query()["code"]
	if !ok || len(keys) == 0 || len(keys[0]) < 1 || keys[0] == "resetcomplete" {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Confirm code does not exist", "msgred": true})
		return
	}
	key := keys[0]
	tag, err := dbpool.Exec(r.Context(), "UPDATE accounts SET email_confirmed = now(), email_confirm_code = '' WHERE email_confirm_code = $1", key)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	if tag.RowsAffected() != 1 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Confirm code does not exist", "msgred": true})
		return
	}
	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Email confirmed.", "msggreen": true})
}

func recoverPasswordHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		err := r.ParseForm()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			log.Printf("Error reading form: %v", err)
			return
		}
		if r.PostFormValue("reset") == "yes" {
			err := r.ParseForm()
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				log.Printf("Error reading form: %v", err)
				return
			}
			log.Printf("code [%v]", r.PostFormValue("code"))
			log.Printf("password [%v]", r.PostFormValue("password"))
			log.Printf("password-confirm [%v]", r.PostFormValue("password-confirm"))
			log.Printf("reset [%v]", r.PostFormValue("reset"))
			if r.PostFormValue("code") == "resetcomplete" || r.PostFormValue("code") == "" || r.PostFormValue("password") == "" {
				basicLayoutLookupRespond("passwordReset", w, r, map[string]any{"RecoverError": true})
				return
			}
			if r.PostFormValue("password") != r.PostFormValue("password-confirm") {
				basicLayoutLookupRespond("passwordReset", w, r, map[string]any{"RecoverDetailedError": "Passwords don't match"})
				return
			}
			if !validatePassword(r.PostFormValue("password")) {
				basicLayoutLookupRespond("passwordReset", w, r, map[string]any{"RecoverDetailedError": "Password must be between 6 and 25 symbols in length"})
				return
			}
			tag, err := dbpool.Exec(r.Context(), "UPDATE accounts SET password = $1, email_confirm_code = 'resetcomplete' WHERE email_confirm_code = $2", hashPassword(r.PostFormValue("password")), r.PostFormValue("code"))
			if err != nil {
				basicLayoutLookupRespond("passwordReset", w, r, map[string]any{"RecoverError": true})
				notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
				return
			}
			if tag.RowsAffected() != 1 {
				basicLayoutLookupRespond("passwordReset", w, r, map[string]any{"RecoverError": true})
				log.Print("No such recovery code")
				return
			}
			log.Print("Password recovery attempt SUCCESS")
			basicLayoutLookupRespond("passwordReset", w, r, map[string]any{"RecoverComplete": true})
			w.Header().Set("Refresh", "5; /login")
		} else {
			if !validateEmail(r.PostFormValue("email")) {
				basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverError": true})
				return
			}
			reqTerminated := false
			err := dbpool.QueryRow(r.Context(), "SELECT terminated FROM accounts WHERE email = $1 AND coalesce(extract(epoch from email_confirmed), 0) != 0", r.PostFormValue("email")).Scan(&reqTerminated)
			if err != nil {
				basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverError": true})
				if !errors.Is(err, pgx.ErrNoRows) {
					notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
				}
				return
			}
			if reqTerminated {
				basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverError": true})
				return
			}
			reqemailcode := generateRandomString(50)
			tag, err := dbpool.Exec(r.Context(), "UPDATE accounts SET email_confirm_code = $1 WHERE email = $2", reqemailcode, r.PostFormValue("email"))
			if err != nil {
				basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverError": true})
				notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
				return
			}
			if tag.RowsAffected() != 1 {
				basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverError": true})
				notifyErrorWebhook(fmt.Sprintf("%s\n%s", tag.String(), string(debug.Stack())))
				return
			}
			log.Printf("Password recovery attempt [%s]", r.PostFormValue("email"))
			sendgridRecoverRequest(r.PostFormValue("email"), reqemailcode)
			basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverComplete": true, "Email": r.PostFormValue("email")})
		}
	} else {
		keys, ok := r.URL.Query()["code"]
		if !ok || len(keys[0]) < 1 {
			basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{})
			log.Print("No code")
			return
		}
		numEmails := 0
		numEmailsErr := dbpool.QueryRow(r.Context(), "SELECT COUNT(*) FROM accounts WHERE email_confirm_code = $1", keys[0]).Scan(&numEmails)
		if numEmailsErr != nil {
			basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverError": true})
			notifyErrorWebhook(fmt.Sprintf("%s\n%s", numEmailsErr.Error(), string(debug.Stack())))
			return
		}
		if numEmails != 1 {
			basicLayoutLookupRespond("recoveryRequest", w, r, map[string]any{"RecoverError": true})
			return
		}
		basicLayoutLookupRespond("passwordReset", w, r, map[string]any{"RecoveryCode": keys[0]})
	}
}

func accountHandler(w http.ResponseWriter, r *http.Request) {
	var allow_host_request bool
	err := dbpool.QueryRow(r.Context(), `select allow_host_request from accounts where username = $1`, sessionGetUsername(r)).Scan(&allow_host_request)
	if DBErr(w, r, err) {
		return
	}

	var game_count int
	err = dbpool.QueryRow(r.Context(), `select
	count(*) filter (where g.calculated and not g.deleted and coalesce(rc.is_pve, false) = false)
from players p
join games g on g.id = p.game
left join games_rating_categories grc on grc.game = g.id
left join rating_categories rc on rc.id = grc.category
join identities i on i.id = p.identity
join accounts a on a.id = i.account
where a.username = $1`, sessionGetUsername(r)).Scan(&game_count)
	if DBErr(w, r, err) {
		return
	}

	var selected_name *string
	err = dbpool.QueryRow(r.Context(), `select n.display_name
from names as n
join accounts as a on a.name = n.id
where a.username = $1`, sessionGetUsername(r)).Scan(&selected_name)
	if !errors.Is(err, pgx.ErrNoRows) {
		if DBErr(w, r, err) {
			return
		}
	}

	var identities_count int
	err = dbpool.QueryRow(r.Context(), `select count(*) filter (where i.pkey is not null)
from identities i
join accounts a on a.id = i.account
where a.username = $1`, sessionGetUsername(r)).Scan(&identities_count)
	if DBErr(w, r, err) {
		return
	}

	basicLayoutLookupRespond("accountSettings", w, r, map[string]any{
		"ModeratorStatus": allow_host_request,
		"GameCount":       game_count,
		"SelectedName":    selected_name,
		"IdentitiesCount": identities_count,
	})
}
