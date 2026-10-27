package main

// go get firebase.google.com/go/v4 google.golang.org/api

import (
	"context"
	"fmt"
	"html"
	"log"
	"regexp"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/gofiber/fiber/v2"
	"google.golang.org/api/option"
)

var fcmClient *messaging.Client

// siteBase must match the host the Android app loads (SITE_BASE_URL in
// MainActivity.kt). If the app uses www.dostana.biz and this uses the
// apex, a tapped notification can land on a logged-out page unless the
// server redirects between them with cookies intact.
const siteBase = "https://dostana.biz"

// InitFCM must run once at startup (called from main()), after the
// Firebase console service-account JSON has been uploaded to the VPS.
func InitFCM(serviceAccountPath string) error {
	opt := option.WithCredentialsFile(serviceAccountPath)
	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		return err
	}
	fcmClient, err = app.Messaging(context.Background())
	return err
}

// SendPushNotification delivers a push to one device. deepLink should be a
// full https://dostana.biz/... URL — the Android app loads it directly when
// the notification is tapped.
func SendPushNotification(token, title, body, deepLink string) error {
	if fcmClient == nil {
		return nil // InitFCM never ran (or failed) — don't panic on a nil client
	}

	msg := &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: map[string]string{
			"deep_link": deepLink,
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",
		},
	}
	_, err := fcmClient.Send(context.Background(), msg)
	if err != nil {
		log.Printf("[fcm] send failed: %v", err)
		// A token that's uninstalled/expired returns a specific
		// "registration-token-not-registered" error from FCM — when that
		// happens, the token is dead forever and will keep failing (and
		// keep costing a network round trip) on every future notification
		// until it's removed.
		if messaging.IsRegistrationTokenNotRegistered(err) || messaging.IsUnregistered(err) {
			if _, delErr := db.Exec(context.Background(),
				`DELETE FROM device_tokens WHERE fcm_token = $1`, token); delErr != nil {
				log.Printf("[fcm] failed to prune dead token: %v", delErr)
			}
		}
	}
	return err
}

// RegisterDeviceToken handles POST /api/device-token from the Android app.
// Uses the same getCurrentUser(c) every other handler in this codebase
// uses, so it's consistent with your session/cookie auth.
func RegisterDeviceToken(c *fiber.Ctx) error {
	username := getCurrentUser(c)
	if username == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "not logged in"})
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := c.BodyParser(&body); err != nil || body.Token == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "missing token"})
	}
	if len(body.Token) > 4096 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid token"})
	}

	_, err := db.Exec(context.Background(), `
        INSERT INTO device_tokens (username, fcm_token, updated_at)
        VALUES ($1, $2, now())
        ON CONFLICT (username, fcm_token) DO UPDATE SET updated_at = now()
    `, username, body.Token)
	if err != nil {
		log.Printf("RegisterDeviceToken db error for %s: %v", username, err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "db error"})
	}

	return c.JSON(fiber.Map{"ok": true})
}

// NotifyUser pushes to every device this username is signed in on. Call
// this from wherever the event actually happens — e.g. right after you
// insert a row into notifications/chat_notifications.
//
// NOTE: the token lookup below is a synchronous DB query, so don't call
// this directly on a request path — use Push() instead, which wraps it
// in goSafe.
func NotifyUser(username, title, body, deepLink string) {
	if username == "" {
		return
	}

	qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := db.Query(qctx,
		`SELECT fcm_token FROM device_tokens WHERE username = $1`, username)
	if err != nil {
		log.Printf("[fcm] token lookup failed for %s: %v", username, err)
		return
	}
	defer rows.Close()

	var tokens []string
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			tokens = append(tokens, t)
		}
	}

	for _, t := range tokens {
		go SendPushNotification(t, title, body, deepLink)
	}
}

// ─────────────────────────────────────────────────────────────────────────
// PUSH HELPERS — used by every notification site in the app
// ─────────────────────────────────────────────────────────────────────────

var pushTagStripper = regexp.MustCompile(`<[^>]*>`)

// cleanPushText turns stored notification content (which often contains
// HTML like <b>@user</b> and escaped entities, because it's written for
// the web notifications feed) into plain text safe for the Android tray.
func cleanPushText(s string) string {
	s = pushTagStripper.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

// Push sends one FCM push in the background. Title = sender username,
// matching the convention the existing chat NotifyUser calls already use.
// Safe to call from a request handler: the DB lookup happens off-thread.
func Push(recipient, sender, body, link string) {
	if recipient == "" || strings.EqualFold(recipient, sender) {
		return
	}
	if link == "" {
		link = siteBase + "/notifications"
	}
	cleanBody := cleanPushText(body)
	goSafe("push", func() { NotifyUser(recipient, sender, cleanBody, link) })
}

// PushMehfilMembers fans a push out to every member of a mehfil except the
// sender — used for broadcasts, topic/rules/fee updates, etc.
func PushMehfilMembers(mehfilID interface{}, sender, body, link string) {
	cleanBody := cleanPushText(body)
	goSafe("push mehfil members", func() {
		qctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		rows, err := db.Query(qctx,
			"SELECT username FROM mehfil_members WHERE mehfil_id=$1 AND username<>$2",
			mehfilID, sender)
		if err != nil {
			log.Printf("[fcm] mehfil member lookup failed for %v: %v", mehfilID, err)
			return
		}

		var users []string
		for rows.Next() {
			var u string
			if rows.Scan(&u) == nil {
				users = append(users, u)
			}
		}
		rows.Close()

		for _, u := range users {
			NotifyUser(u, sender, cleanBody, link)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────────
// DEEP-LINK BUILDERS
// ─────────────────────────────────────────────────────────────────────────

func postLink(postID int) string  { return fmt.Sprintf("%s/post/%d", siteBase, postID) }
func profileLink(u string) string { return siteBase + "/profile/" + u }
func chatLink(u string) string    { return siteBase + "/chat/" + u }
func invitesLink() string         { return siteBase + "/invites" }
func notificationsLink() string   { return siteBase + "/notifications" }

func mehfilLink(id interface{}) string { return fmt.Sprintf("%s/view/%v", siteBase, id) }

func mehfilMsgLink(id interface{}, msgID int) string {
	return fmt.Sprintf("%s/view/%v#msg-%d", siteBase, id, msgID)
}