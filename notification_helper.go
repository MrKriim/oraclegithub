package main

import "log"

// CreateNotification - Helper to insert notifications cleanly
func CreateNotification(recipient, sender, notifType string, postID, commentID int, content string) {
	if recipient == sender || recipient == "" {
		return
	}

	_, err := db.Exec(ctx, `
        INSERT INTO notifications (recipient, sender, type, post_id, comment_id, content)
        VALUES ($1, $2, $3, $4, $5, $6)`,
		recipient, sender, notifType, postID, commentID, content)

	if err != nil {
		log.Println("Failed to create notification:", err)
	}
}