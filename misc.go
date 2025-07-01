package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"golang.org/x/crypto/bcrypt"
)

func btoi(a uint32) bool {
	return a != 0
}

//lint:ignore U1000 for performance
func measureHandlerTimings(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := time.Now()
		h(w, r)
		log.Printf("Timings: %v", time.Since(s))
	}
}

func parseFormString(r *http.Request, field string, re *regexp.Regexp) *string {
	f := r.FormValue(field)
	if f == "" {
		return nil
	}
	if re != nil {
		if !re.MatchString(f) {
			return nil
		}
	}
	return &f
}

func parseFormInt(r *http.Request, field string) *int {
	f := r.FormValue(field)
	if f == "" {
		return nil
	}
	ret, err := strconv.Atoi(f)
	if err != nil {
		return nil
	}
	return &ret
}

func parseFormIntWhitelist(r *http.Request, field string, whitelist ...int) *int {
	i := parseFormInt(r, field)
	if i == nil {
		return nil
	}
	for _, v := range whitelist {
		if v == *i {
			return i
		}
	}
	return nil
}

func parseFormBool(r *http.Request, field string) bool {
	f := r.FormValue(field)
	return f == "on" || f == "true" || f == "checked"
}

func parseQueryInt(r *http.Request, field string, d int) int {
	if val, ok := r.URL.Query()[field]; ok && len(val) > 0 {
		val2, err := strconv.Atoi(val[0])
		if err == nil {
			return val2
		}
	}
	return d
}

func parseQueryString(r *http.Request, field string, d string) string {
	if val, ok := r.URL.Query()[field]; ok && len(val) > 0 {
		return val[0]
	}
	return d
}

func parseQueryStringFiltered(r *http.Request, field string, d string, variants ...string) string {
	if val, ok := r.URL.Query()[field]; ok && len(val) > 0 {
		for _, v := range variants {
			if val[0] == v {
				return v
			}
		}
	}
	return d
}

func parseQueryStringMapped(r *http.Request, field string, d string, m map[string]string) string {
	if val, ok := r.URL.Query()[field]; ok && len(val) > 0 {
		v, ok := m[val[0]]
		if ok {
			return v
		}
	}
	return d
}

func stringOneOf(a string, b ...string) bool {
	for _, s := range b {
		if a == s {
			return true
		}
	}
	return false
}

func notifyErrorWebhook(msg string) {
	log.Println(msg)
	if strings.Contains(msg, "cannot scan timestamp (OID 1114) in binary format into **time.Time") {
		return
	}
	err := sendWebhook(cfg.GetDSString("", "webhooks", "errors"), msg)
	if err != nil {
		log.Println(err)
	}
}

func respondWithCodeAndPlaintext(w http.ResponseWriter, code int, resp string) {
	w.WriteHeader(code)
	w.Write([]byte(html.EscapeString(resp)))
}

func logRespondWithCodeAndPlaintext(w http.ResponseWriter, code int, resp string) {
	log.Println(resp)
	respondWithCodeAndPlaintext(w, code, html.EscapeString(resp))
}

func respondWithUnauthorized(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
	basicLayoutLookupRespond(templateNotAuthorized, w, r, map[string]any{})
}

func respondWithForbidden(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusForbidden)
	basicLayoutLookupRespond(templateErrorForbidden, w, r, map[string]any{})
}

func respondWithNotImplemented(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
	basicLayoutLookupRespond(templatePlainMessage, w, r, map[string]any{"msg": "Not implemented"})
}

func checkFormParse(w http.ResponseWriter, r *http.Request) bool {
	err := r.ParseForm()
	if err != nil {
		basicLayoutLookupRespond(templatePlainMessage, w, r, map[string]any{"msgred": true, "msg": "Form parse error: " + err.Error()})
	}
	return err == nil
}

//lint:ignore U1000 for future
func checkRespondDatabaseErrorAny(w http.ResponseWriter, r *http.Request, derr error) bool {
	if derr != nil {
		basicLayoutLookupRespond(templatePlainMessage, w, r, map[string]any{"msgred": true, "msg": "Database query error: " + derr.Error()})
	}
	return derr == nil
}

func checkRespondGenericErrorAny(w http.ResponseWriter, r *http.Request, derr error) bool {
	if derr != nil {
		basicLayoutLookupRespond(templatePlainMessage, w, r, map[string]any{"msgred": true, "msg": "Error: " + derr.Error()})
	}
	return derr == nil
}

func myNotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			basicLayoutLookupRespond("error404", w, r, map[string]any{})
		}
	})
}
func hashPassword(pwd string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		log.Println(err)
		return ""
	}
	return string(hash)
}
func comparePasswords(hashedPwd string, plainPwd string) bool {
	if hashedPwd == "nologin" {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(hashedPwd), []byte(plainPwd))
	if err != nil {
		log.Println(err)
		return false
	}
	return true
}
func generateRandomString(slen int) string {
	s := ""
	a := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	al := len(a)
	for i := 0; i < slen; i++ {
		s += string(a[rand.Intn(al-1)])
	}
	return s
}

func validateUsername(u string) bool {
	if len(u) < 3 || len(u) > 25 {
		return false
	}
	if strings.Contains(u, "@") {
		return false
	}
	return true
}
func validatePassword(u string) bool {
	if len(u) < 6 || len(u) > 25 {
		return false
	}
	return true
}

var regexEmail = regexp.MustCompile("^[a-zA-Z0-9.!#$%&'*+\\/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$")

func validateEmail(u string) bool {
	if len(u) < 3 || len(u) > 254 {
		return false
	}
	return regexEmail.MatchString(u)
}

func sendgridConfirmcode(email string, code string) error {
	if email == "noemail" {
		return nil
	}
	sendstr := fmt.Sprintf(`{
	"personalizations": [
		{
			"to": [
				{
					"email":"%s"
				}
			]
		}
	],
	"from": {
		"email": "no-reply@wz2100-autohost.net",
		"name": "Account Registration"
	},
	"subject": "Welcome to Warzone 2100 Autohoster website",
	"content": [
		{
			"type": "text/plain",
		 	"value": "Welcome to Warzone 2100 Autohoster. To confirm your email address follow this link: https://wz2100-autohost.net/activate?code=%s"
		},
		{
			"type":"text/html",
			"value":"<html><h3>Welcome to Warzone 2100 Autohoster.</h3><p>To confirm your email address follow link below.</p><p><a href=\"https://wz2100-autohost.net/activate?code=%s\">Activate account</a></p></html>"
		}
	]
}`, email, code, code)
	req, err := http.NewRequest("POST", "https://api.sendgrid.com/v3/mail/send", bytes.NewBuffer([]byte(sendstr)))
	if err != nil {
		return err
	}
	skey, ok := cfg.GetString("sendgridKey")
	if !ok {
		log.Println("Sendgrid key is not set!")
		return errors.New("missconfigured server (let admin know)")
	}
	req.Header.Set("Authorization", "Bearer "+skey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	log.Println("response Status:", resp.Status)
	log.Println("response Headers:", resp.Header)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	log.Println("response Body:", string(body))
	if resp.StatusCode == 200 || resp.StatusCode == 202 {
		return nil
	}
	return nil
}

func sendgridRecoverRequest(email string, code string) bool {
	sendstr := heredoc.Docf(`
{
	"personalizations": [
		{
			"to": [
				{
					"email":"%s"
				}
			]
		}
	],
	"from": {
		"email": "no-reply@wz2100-autohost.net",
		"name": "Autohoster"
	},
	"subject": "Password recovery",
	"content": [
		{
			"type": "text/plain",
		 	"value": "Hello, to reset your password please follow this link: https://wz2100-autohost.net/recover?code=%s\nIf this was not you and you think someone is trying to gain access to your account please contact us."
		},
		{
			"type":"text/html",
			"value":"<html><h3>Password reset</h3><p>To reset your password follow link below.</p><p><a href=\"https://wz2100-autohost.net/recover?code=%s\">Set new password</a></p><p>If this was not you and you think someone is trying to gain access to your account please contact us.</p></html>"
		}
	]
}`, email, code, code)
	req, _ := http.NewRequest("POST", "https://api.sendgrid.com/v3/mail/send", bytes.NewBuffer([]byte(sendstr)))
	skey, ok := cfg.GetString("sendgridKey")
	if !ok {
		log.Println("Sendgrid key is not set!")
		return false
	}
	req.Header.Set("Authorization", "Bearer "+skey)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Println(err)
		return false
	}
	defer resp.Body.Close()
	log.Println("response Status:", resp.Status)
	log.Println("response Headers:", resp.Header)
	body, _ := io.ReadAll(resp.Body)
	log.Println("response Body:", string(body))
	return resp.Status == "200 Success" || resp.Status == "202 Accepted"
}

func isAprilFools() bool {
	t := time.Now()
	return t.Month() == 4 && ((t.Day() == 1 && t.Hour() >= 2) || (t.Day() == 2 && t.Hour() < 2))
}

func escapeBacktick(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\n", "\\n"), "`", "\\`")
}

func sendWebhook(url, content string) error {
	if url == "" {
		return errors.New("url is empty")
	}
	if content == "" {
		return errors.New("content is empty")
	}
	b, err := json.Marshal(map[string]any{
		"username": "Frontend",
		"content":  content,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	c := http.Client{Timeout: 5 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return fmt.Errorf(string(responseBody))
	}
	return nil
}
