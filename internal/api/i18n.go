package api

import (
	"embed"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFS embed.FS

var bundle *i18n.Bundle

func init() {
	bundle = i18n.NewBundle(language.English)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
	if _, err := bundle.LoadMessageFileFS(localeFS, "locales/en.json"); err != nil {
		panic(err)
	}
	if _, err := bundle.LoadMessageFileFS(localeFS, "locales/zh.json"); err != nil {
		panic(err)
	}
}

const localizerKey = "i18n_localizer"

func i18nMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		lang := c.GetHeader("Accept-Language")
		localizer := i18n.NewLocalizer(bundle, lang, "en")
		c.Set(localizerKey, localizer)
		c.Next()
	}
}

func localize(c *gin.Context, messageID string) string {
	l := c.MustGet(localizerKey).(*i18n.Localizer)
	msg, err := l.Localize(&i18n.LocalizeConfig{MessageID: messageID})
	if err != nil {
		return messageID
	}
	return msg
}

func localizeWithData(c *gin.Context, messageID string, data any) string {
	l := c.MustGet(localizerKey).(*i18n.Localizer)
	msg, err := l.Localize(&i18n.LocalizeConfig{
		MessageID:    messageID,
		TemplateData: data,
	})
	if err != nil {
		return messageID
	}
	return msg
}
