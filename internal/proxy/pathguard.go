package proxy

import (
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// PathTraversalGuard rejects any request whose path is not already in
// canonical form — dot-segments ("..", "."), duplicate slashes, backslashes.
//
// The proxy authorizes the Gin :id route param but forwards the RAW path to
// Daytona under the admin bearer. Gin matches on the decoded path, so both a
// literal "/api/sandbox/<owned>/../<victim>/ssh-access" and its percent-encoded
// form bind :id to the ATTACKER's sandbox while the forwarded path still
// carries the traversal — any upstream (Daytona, an ingress) that normalizes
// dot-segments before routing would then execute the request against the
// VICTIM's sandbox with admin credentials. Whether the current upstream
// normalizes is an off-repo behavior that can change under us; rejecting
// non-canonical paths at the boundary removes the entire class.
//
// No legitimate API path here contains dot-segments: IDs are UUIDs and names
// with interior dots (e.g. "my.app") are untouched — path.Clean only rewrites
// whole "." / ".." segments, duplicate slashes, and trailing slashes (the
// latter preserved below since some clients send them).
func PathTraversalGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		cleaned := path.Clean(p)
		if p != "/" && strings.HasSuffix(p, "/") && !strings.HasSuffix(cleaned, "/") {
			cleaned += "/"
		}
		if cleaned != p || strings.Contains(p, "\\") {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "malformed path"})
			return
		}
		c.Next()
	}
}
