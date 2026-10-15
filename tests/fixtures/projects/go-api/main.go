// Fixture: a small gin service. Kestrel only reads this file; it is never built.
package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	_ = r.Run(":8080")
}
