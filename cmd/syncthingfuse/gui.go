package main

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	human "github.com/dustin/go-humanize"
	"github.com/huaixv/syncthingfuse/lib/autogenerated"
	"github.com/huaixv/syncthingfuse/lib/config"
	"github.com/huaixv/syncthingfuse/lib/model"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/sync"
	"github.com/syncthing/syncthing/lib/tlsutil"
)

var (
	guiAssets = os.Getenv("STGUIASSETS")
)

type apiSvc struct {
	id              protocol.DeviceID
	cfg             *config.Wrapper
	model           *model.Model
	assetDir        string
	listener        net.Listener
	stop            chan struct{}
	configInSync    bool
	systemConfigMut sync.Mutex
}

func newAPISvc(id protocol.DeviceID, cfg *config.Wrapper, model *model.Model) (*apiSvc, error) {
	if guiAssets == "" {
		guiAssets = locations[locGUIAssets]
	}

	svc := &apiSvc{
		id:              id,
		cfg:             cfg,
		model:           model,
		assetDir:        guiAssets,
		systemConfigMut: sync.NewMutex(),
		configInSync:    true,
	}

	var err error
	svc.listener, err = svc.getListener()
	return svc, err
}

func (s *apiSvc) getListener() (net.Listener, error) {
	cert, err := tls.LoadX509KeyPair(locations[locHTTPSCertFile], locations[locHTTPSKeyFile])
	if err != nil {
		l.Infoln("Loading HTTPS certificate:", err)
		l.Infoln("Creating new HTTPS certificate")

		// When generating the HTTPS certificate, use the system host name per
		// default. If that isn't available, use the "syncthing" default.
		var name string
		name, err = os.Hostname()
		if err != nil {
			name = tlsDefaultCommonName
		}

		cert, err = tlsutil.NewCertificate(locations[locHTTPSCertFile], locations[locHTTPSKeyFile], name)
	}
	if err != nil {
		return nil, err
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10, // No SSLv3
		CipherSuites: []uint16{
			// No RC4
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA,
			tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
		},
	}

	rawListener, err := net.Listen("tcp", s.cfg.Raw().GUI.RawAddress)
	if err != nil {
		return nil, err
	}

	listener := &tlsutil.DowngradingListener{rawListener, tlsCfg}
	return listener, nil
}

func (s *apiSvc) getMux() *http.ServeMux {
	mux := http.NewServeMux()

	getApiMux := http.NewServeMux()
	getApiMux.HandleFunc("/api/system/config", s.getSystemConfig)
	getApiMux.HandleFunc("/api/system/config/insync", s.getSystemConfigInSync)
	getApiMux.HandleFunc("/api/system/connections", s.getSystemConnections)
	getApiMux.HandleFunc("/api/system/pins/status", s.getPinStatus)
	getApiMux.HandleFunc("/api/verify/deviceid", s.getDeviceID) // id
	getApiMux.HandleFunc("/api/db/browse", s.getDBBrowse)       // folderID pathPrefix

	postApiMux := http.NewServeMux()
	postApiMux.HandleFunc("/api/system/config", s.postSystemConfig)       // <body>
	postApiMux.HandleFunc("/api/verify/humansize", s.postVerifyHumanSize) // <body>

	apiMux := getMethodHandler(getApiMux, postApiMux)
	mux.Handle("/api/", apiMux)

	// Serve compiled in assets unless an asset directory was set (for development)
	mux.Handle("/", embeddedStatic{
		assetDir: s.assetDir,
		assets:   autogenerated.Assets(),
	})

	return mux
}

func (s *apiSvc) Serve() {
	s.stop = make(chan struct{})

	srv := http.Server{
		Handler:     s.getMux(),
		ReadTimeout: 10 * time.Second,
	}

	l.Infoln("API listening on", s.listener.Addr())
	err := srv.Serve(s.listener)

	// The return could be due to an intentional close. Wait for the stop
	// signal before returning. IF there is no stop signal within a second, we
	// assume it was unintentional and log the error before retrying.
	select {
	case <-s.stop:
	case <-time.After(time.Second):
		l.Warnln("API:", err)
	}
}

func getMethodHandler(get, post http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			get.ServeHTTP(w, r)
		case "POST":
			post.ServeHTTP(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (s *apiSvc) Stop() {
	close(s.stop)
	s.listener.Close()
}

func (s *apiSvc) String() string {
	return fmt.Sprintf("apiSvc@%p", s)
}

func (s *apiSvc) getSystemConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(s.cfg.Raw())
}

func (s *apiSvc) getSystemConfigInSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(s.configInSync)
}

func (s *apiSvc) getSystemConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(s.model.GetConnections())
}

func (s *apiSvc) getPinStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(s.model.GetPinsStatusByFolder())
}

func (s *apiSvc) getDeviceID(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	idStr := qs.Get("id")
	id, err := protocol.DeviceIDFromString(idStr)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err == nil {
		json.NewEncoder(w).Encode(map[string]string{
			"id": id.String(),
		})
	} else {
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
	}
}

func (s *apiSvc) getDBBrowse(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	folderID := qs.Get("folderID")
	pathPrefix := qs.Get("pathPrefix")

	paths := s.model.GetPathsMatchingPrefix(folderID, pathPrefix)

	json.NewEncoder(w).Encode(paths)
}

func (s *apiSvc) postSystemConfig(w http.ResponseWriter, r *http.Request) {
	s.systemConfigMut.Lock()
	defer s.systemConfigMut.Unlock()

	// deserialize
	var to config.Configuration
	err := json.NewDecoder(r.Body).Decode(&to)
	if err != nil {
		l.Warnln("decoding posted config:", err)
		http.Error(w, err.Error(), 500)
		return
	}

	// Activate and save
	err = s.cfg.Replace(to)
	s.configInSync = false
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.cfg.Save()
}

func (s *apiSvc) postVerifyHumanSize(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading body"+err.Error(), 500)
		return
	}

	_, err = human.ParseBytes(string(b))
	if err != nil {
		http.Error(w, "Cannot parse size"+err.Error(), 500)
		return
	}
	return
}

type embeddedStatic struct {
	assetDir string
	assets   map[string][]byte
}

func (s embeddedStatic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Path

	if file[0] == '/' {
		file = file[1:]
	}

	if len(file) == 0 {
		file = "index.html"
	}

	if s.assetDir != "" {
		p := filepath.Join(s.assetDir, filepath.FromSlash(file))
		_, err := os.Stat(p)
		if err == nil {
			http.ServeFile(w, r, p)
			return
		}
	}

	bs, ok := s.assets[file]
	if !ok {
		http.NotFound(w, r)
		return
	}

	if r.Header.Get("If-Modified-Since") == autogenerated.AssetsBuildDate {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	mtype := s.mimeTypeForFile(file)
	if len(mtype) != 0 {
		w.Header().Set("Content-Type", mtype)
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
	} else {
		// ungzip if browser not send gzip accepted header
		var gr *gzip.Reader
		gr, _ = gzip.NewReader(bytes.NewReader(bs))
		bs, _ = ioutil.ReadAll(gr)
		gr.Close()
	}
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(bs)))
	w.Header().Set("Last-Modified", autogenerated.AssetsBuildDate)
	w.Header().Set("Cache-Control", "public")

	w.Write(bs)
}

func (s embeddedStatic) mimeTypeForFile(file string) string {
	// We use a built in table of the common types since the system
	// TypeByExtension might be unreliable. But if we don't know, we delegate
	// to the system.
	ext := filepath.Ext(file)
	switch ext {
	case ".htm", ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".ttf":
		return "application/x-font-ttf"
	case ".woff":
		return "application/x-font-woff"
	case ".svg":
		return "image/svg+xml"
	default:
		return mime.TypeByExtension(ext)
	}
}
