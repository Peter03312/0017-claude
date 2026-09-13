package main

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// newRouter wires the HTTP routes; kept separate from main so tests can use it.
func newRouter() *gin.Engine {
	r := gin.Default()
	r.POST("/assess", assessHandler)
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return r
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := newRouter().Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
