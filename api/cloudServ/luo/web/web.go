package web

import (
	"net/http"
	"strings"

	"github.com/NYTimes/gziphandler"
	l4g "github.com/alecthomas/log4go"
	"github.com/mssola/user_agent"

	"github.com/teddy/sign-in-on/api"
	"github.com/teddy/sign-in-on/app"
	"github.com/teddy/sign-in-on/model"
	"github.com/teddy/sign-in-on/utils"
)

func InitWeb() {
	l4g.Debug(utils.T("web.init.debug"))

	mainrouter := app.Srv.Router

	if *utils.Cfg.ServiceSettings.WebserverMode != "disabled" {
		clientDir, _ := utils.FindDir(model.CLIENT_DIR)
		staticDir := clientDir + "static"
		l4g.Debug("Using client directory at %v", staticDir)
		if *utils.Cfg.ServiceSettings.WebserverMode == "gzip" {
			mainrouter.PathPrefix("/static/").Handler(gziphandler.GzipHandler(staticHandler(http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))))
		} else {
			mainrouter.PathPrefix("/static/").Handler(staticHandler(http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir)))))
		}

		mainrouter.Handle("/{anything:.*}", api.AppHandler(root)).Methods("GET")
	}
}

func staticHandler(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=31556926, public")
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	})
}

var browsersNotSupported string = "MSIE/8;MSIE/9;MSIE/10;Internet Explorer/8;Internet Explorer/9;Internet Explorer/10;Safari/7;Safari/8"

func CheckBrowserCompatability(c *api.Context, r *http.Request) bool {
	ua := user_agent.New(r.UserAgent())
	bname, bversion := ua.Browser()

	browsers := strings.Split(browsersNotSupported, ";")
	for _, browser := range browsers {
		version := strings.Split(browser, "/")

		if strings.HasPrefix(bname, version[0]) && strings.HasPrefix(bversion, version[1]) {
			return false
		}
	}

	return true

}

func root(c *api.Context, w http.ResponseWriter, r *http.Request) {
	if !CheckBrowserCompatability(c, r) {
		w.WriteHeader(http.StatusBadRequest)
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(c.T("web.check_browser_compatibility.app_error")))
		return
	}

	if api.IsApiCall(r) {
		api.Handle404(w, r)
		return
	}

	w.Header().Set("Cache-Control", "no-cache, max-age=31556926, public")

	clientDir, _ := utils.FindDir(model.CLIENT_DIR)
	http.ServeFile(w, r, clientDir+"index.html")
}
