package platform

import (
    "context"
    "log"
    "os"

    fstorage "github.com/gofiber/storage/redis/v3" // Fiber Storage
    goredis "github.com/redis/go-redis/v9"         // Standard driver
)

// Redis uses the Fiber Storage type
var Redis *fstorage.Storage

// RDB uses the raw Go-Redis Client type
var RDB *goredis.Client
var Ctx = context.Background()

func InitRedis() {
    // 1. Get Host and Port from Environment
    // Changed "cache" to "127.0.0.1" for bare-metal VPS setup
    redisHost := os.Getenv("REDIS_HOST")
    if redisHost == "" {
        redisHost = "127.0.0.1"
    }

    redisPort := os.Getenv("REDIS_PORT")
    if redisPort == "" {
        redisPort = "6379"
    }

    addr := redisHost + ":" + redisPort

    // 2. Setup Fiber Storage (For Session/Limiter)
    // We pass the host and port separately as the Fiber driver expects
    Redis = fstorage.New(fstorage.Config{
        Host: redisHost,
        Port: 6379, 
    })

    // 3. Setup Raw Go-Redis Client (For custom logic)
    RDB = goredis.NewClient(&goredis.Options{
        Addr: addr,
    })

    // 4. Critical: Test the connection
    if err := RDB.Ping(Ctx).Err(); err != nil {
        log.Fatalf("❌ Redis Connection Failed at %s: %v", addr, err)
    }

    log.Println("✅ Redis Dual-Client Initialized successfully at " + addr)
}