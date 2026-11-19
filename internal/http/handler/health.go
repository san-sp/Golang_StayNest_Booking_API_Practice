package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// DBPinger defines the database health-check behavior
// required by the readiness handler.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// Health checks whether the application is running.
func Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}

// Ready checks whether the application can access PostgreSQL.
func Ready(db DBPinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := db.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "not ready",
				"error":  "database unavailable",
			})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
		})
	}
}
