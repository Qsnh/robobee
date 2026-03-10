package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestI18nMiddleware_English(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(i18nMiddleware())
	r.GET("/test", func(c *gin.Context) {
		msg := localize(c, "WorkerNotFound")
		c.String(http.StatusOK, msg)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Language", "en")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "worker not found" {
		t.Errorf("expected 'worker not found', got %q", w.Body.String())
	}
}

func TestI18nMiddleware_Chinese(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(i18nMiddleware())
	r.GET("/test", func(c *gin.Context) {
		msg := localize(c, "WorkerNotFound")
		c.String(http.StatusOK, msg)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Accept-Language", "zh")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "未找到工作者" {
		t.Errorf("expected '未找到工作者', got %q", w.Body.String())
	}
}

func TestI18nMiddleware_FallbackToEnglish(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(i18nMiddleware())
	r.GET("/test", func(c *gin.Context) {
		msg := localize(c, "WorkerNotFound")
		c.String(http.StatusOK, msg)
	})

	req := httptest.NewRequest("GET", "/test", nil)
	// No Accept-Language header — should fall back to English
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Body.String() != "worker not found" {
		t.Errorf("expected English fallback, got %q", w.Body.String())
	}
}
