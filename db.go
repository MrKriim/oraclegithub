package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
)

// Global variables for database and redis
var (
	ctx = context.Background()
	rdb *redis.Client
	db  *pgxpool.Pool
)

// Helper function to get environment variables
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func initDatabase() {
	// 1. Load environment variables
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// 2. Initialize Redis
	redisAddr := getEnv("REDIS_ADDR", "cache:6379") // Matches docker-compose service name
	rdb = redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	
	// Test Redis connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("Warning: Redis not reachable at %s: %v", redisAddr, err)
	}

	// 3. Database Configuration
	dbUser := getEnv("DB_USER", "user")
	dbPass := getEnv("DB_PASSWORD", "password")
	dbHost := getEnv("DB_HOST", "db") // Matches docker-compose service name
	dbPort := getEnv("DB_PORT", "5432")
	dbName := getEnv("DB_NAME", "social_media_db")

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		dbUser, dbPass, dbHost, dbPort, dbName)

	// 4. Create Database Connection Pool
	// We use a timeout context for the connection attempt
	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err = pgxpool.New(connectCtx, connStr)
	if err != nil {
		log.Fatal("Unable to create connection pool:", err)
	}

	// Ping the pool to ensure it's actually alive
	if err := db.Ping(connectCtx); err != nil {
		log.Fatal("Failed to ping database pool. Is the DB container ready?:", err)
	}

	log.Println("Connected to PostgreSQL Pool successfully!")



	// Create users table
	_, err = db.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS users (
            id SERIAL PRIMARY KEY,
            username TEXT UNIQUE NOT NULL,
            password TEXT NOT NULL,
            bio TEXT,
            city TEXT,
            gender TEXT,
            birth_date DATE,
            points INT DEFAULT 0,
            profile_image TEXT DEFAULT '',
            last_profile_image_change TIMESTAMP,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    `)
	if err != nil {
		log.Fatal("Failed to create users table:", err)
	}

	// Create posts table
	_, err = db.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS posts (
            id SERIAL PRIMARY KEY,
            content TEXT,
            author TEXT,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    `)
	if err != nil {
		log.Fatal("Failed to create posts table:", err)
	}

	// Create comments/replies table
	_, err = db.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS comments (
            id SERIAL PRIMARY KEY,
            post_id INT REFERENCES posts(id) ON DELETE CASCADE,
            author TEXT NOT NULL,
            content TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            parent_comment_id INT REFERENCES comments(id) ON DELETE CASCADE
        )
    `)
	if err != nil {
		log.Fatal("Failed to create comments table:", err)
	}

	// Create notifications table
	_, err = db.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS notifications (
            id SERIAL PRIMARY KEY,
            recipient TEXT NOT NULL,
            sender TEXT NOT NULL,
            type TEXT NOT NULL,
            post_id INT REFERENCES posts(id) ON DELETE CASCADE,
            comment_id INT REFERENCES comments(id) ON DELETE CASCADE,
            content TEXT,
            is_read BOOLEAN DEFAULT FALSE,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        )
    `)
	if err != nil {
		log.Fatal("Failed to create notifications table:", err)
	}

	// Create indexes for performance
	_, err = db.Exec(ctx, `
        CREATE INDEX IF NOT EXISTS idx_comments_post_id ON comments(post_id)
    `)
	if err != nil {
		log.Println("Warning: Could not create idx_comments_post_id:", err)
	}

	_, err = db.Exec(ctx, `
        CREATE INDEX IF NOT EXISTS idx_comments_parent_comment_id ON comments(parent_comment_id)
    `)
	if err != nil {
		log.Println("Warning: Could not create idx_comments_parent_comment_id:", err)
	}

	_, err = db.Exec(ctx, `
        CREATE INDEX IF NOT EXISTS idx_notifications_recipient ON notifications(recipient)
    `)
	if err != nil {
		log.Println("Warning: Could not create idx_notifications_recipient:", err)
	}

	_, err = db.Exec(ctx, `
        CREATE INDEX IF NOT EXISTS idx_notifications_is_read ON notifications(is_read)
    `)
	if err != nil {
		log.Println("Warning: Could not create idx_notifications_is_read:", err)
	}

	// Add foreign key constraint for post author (optional but recommended)
	_, err = db.Exec(ctx, `
        DO $$ 
        BEGIN 
            IF NOT EXISTS (
                SELECT 1 FROM pg_constraint 
                WHERE conname = 'fk_posts_author' 
                AND conrelid = 'posts'::regclass
            ) THEN
                ALTER TABLE posts 
                ADD CONSTRAINT fk_posts_author 
                FOREIGN KEY (author) REFERENCES users(username) ON DELETE CASCADE;
            END IF;
        END $$;
    `)
	if err != nil {
		log.Println("Warning: Could not add foreign key to posts:", err)
	}

	// Add foreign key constraint for comment author
	_, err = db.Exec(ctx, `
        DO $$ 
        BEGIN 
            IF NOT EXISTS (
                SELECT 1 FROM pg_constraint 
                WHERE conname = 'fk_comments_author' 
                AND conrelid = 'comments'::regclass
            ) THEN
                ALTER TABLE comments 
                ADD CONSTRAINT fk_comments_author 
                FOREIGN KEY (author) REFERENCES users(username) ON DELETE CASCADE;
            END IF;
        END $$;
    `)

	log.Println("Database tables and indexes initialized successfully!")
}