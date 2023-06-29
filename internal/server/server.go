/*
 * Copyright 2020-2023 Luke Whritenour, Jack Dorland

 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at

 *     http://www.apache.org/licenses/LICENSE-2.0

 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package server

import (
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/jmoiron/sqlx"
	"github.com/orca-group/spirit/internal/config"
	"github.com/orca-group/spirit/internal/util"
	"github.com/rs/zerolog/log"
)

type Server struct {
	Router   *chi.Mux
	Config   *config.Cfg
	Database *sqlx.DB
}

func NewServer(config *config.Cfg, db *sqlx.DB) *Server {
	s := &Server{}
	s.Router = chi.NewRouter()
	s.Config = config
	s.Database = db
	return s
}

// serveFiles conveniently sets up a http.FileServer handler to serve
// static files from a http.FileSystem.
func serveFiles(r chi.Router, path string, root http.FileSystem) {
	if strings.ContainsAny(path, "{}*") {
		panic("FileServer does not permit any URL parameters.")
	}

	if path != "/" && path[len(path)-1] != '/' {
		r.Get(path, http.RedirectHandler(path+"/", http.StatusMovedPermanently).ServeHTTP)
		path += "/"
	}

	path += "*"

	r.Get(path, func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
		fs := http.StripPrefix(pathPrefix, http.FileServer(root))
		fs.ServeHTTP(w, r)
	})
}

// These functions should be executed in the order they are defined, that is:
//  1. Mount middleware - MountMiddleware()
//  2. Add security headers - RegisterHeaders()
//  3. Load static content, if enabled - MountStatic()
//  4. Mount API routes - MountHandlers()

func (s *Server) MountMiddleware() {
	// Register middleware
	s.Router.Use(util.Logger)
	s.Router.Use(middleware.RequestID)
	s.Router.Use(middleware.RealIP)
	s.Router.Use(middleware.AllowContentType("application/json", "multipart/form-data"))

	// Ratelimiter
	reqs, per, err := util.ParseRatelimiterString(s.Config.Ratelimiter)

	if err != nil {
		log.Error().
			Err(err).
			Msg("Parse Ratelimiter Error")
	}

	s.Router.Use(httprate.LimitAll(reqs, per))
	s.Router.Use(middleware.Heartbeat("/ping"))
	s.Router.Use(middleware.Recoverer)

	// CORS
	s.Router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300, // Maximum value not ignored by any of major browsers
	}))
}

func (s *Server) RegisterHeaders() {
	s.Router.Use(middleware.SetHeader("X-Download-Options", "noopen"))
	s.Router.Use(middleware.SetHeader("X-DNS-Prefetch-Control", "off"))
	s.Router.Use(middleware.SetHeader("X-Frame-Options", "SAMEORIGIN"))
	s.Router.Use(middleware.SetHeader("X-XSS-Protection", "1; mode=block"))
	s.Router.Use(middleware.SetHeader("X-Content-Type-Options", "nosniff"))
	s.Router.Use(middleware.SetHeader("Referrer-Policy", "no-referrer-when-downgrade"))
	s.Router.Use(middleware.SetHeader("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload"))
	s.Router.Use(middleware.SetHeader("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; script-src 'self' 'unsafe-inline';"))
}

func (s *Server) MountStatic() {
	// Static content views and homepage
	filesDir := http.Dir("./web/static")
	serveFiles(s.Router, "/static/", filesDir)

	s.Router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		file, err := os.ReadFile("./web/index.html")

		if err != nil {
			util.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		w.Write(file)
	})
}

func (s *Server) MountHandlers() {
	// Register routes
	s.Router.Get("/config", s.GetConfig)

	s.Router.Post("/api/", s.CreateDocument)
	s.Router.Get("/api/{document}", s.FetchDocument)
	s.Router.Get("/api/{document}/raw", s.FetchRawDocument)

	s.Router.Post("/", s.StaticCreateDocument)
	s.Router.Get("/{document}", s.StaticDocument)

	// Legacy routes
	s.Router.Post("/v1/documents/", s.CreateDocument)
	s.Router.Get("/v1/documents/{document}", s.FetchDocument)
	s.Router.Get("/v1/documents/{document}/raw", s.FetchRawDocument)
}
