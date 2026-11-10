package main

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const TOKEN_EXPIRATION_TIME = 24 * time.Hour

type Server struct {
	Store  *Store
	JWTKey []byte
}

func (s *Server) GetHandler(static fs.FS) http.Handler {
	protected := http.NewServeMux()
	protected.HandleFunc("GET /tasks", s.listTasks)
	protected.HandleFunc("POST /tasks", s.createTask)
	protected.HandleFunc("GET /tasks/{id}", s.getTask)
	protected.HandleFunc("POST /tasks/{id}", s.switchTask)
	protected.HandleFunc("PATCH /tasks/{id}", s.changeTask)
	protected.HandleFunc("DELETE /tasks/{id}", s.deleteTask)
	protected.HandleFunc("GET /me", s.UserFromAuth)
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(static))
	mux.Handle("/api/", http.StripPrefix("/api", s.auth(protected)))
	mux.HandleFunc("POST /auth/register", s.register)
	mux.HandleFunc("POST /auth/loginAPI", s.loginWriteToken)
	mux.HandleFunc("POST /auth/login", s.loginSendCookie)
	return mux
}

func (s *Server) UserFromAuth(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	user, err := s.Store.GetUser(r.Context(), userID)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := json.MarshalWrite(w, user); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	tasks, err := s.Store.ListUserTasks(r.Context(), userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := json.MarshalWrite(w, tasks); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}

type TaskJSON struct {
	Body string `json:"body"`
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024*10)
	userId := getUserID(r)
	tbody := TaskJSON{}
	if err := json.UnmarshalRead(r.Body, &tbody, json.RejectUnknownMembers(true)); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	task, err := s.Store.CreateTask(r.Context(), userId, tbody.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := json.MarshalWrite(w, task); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) getTask(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID := getUserID(r)
	task, err := s.Store.GetTask(r.Context(), id, userID)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := json.MarshalWrite(w, task); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}

type TaskJSONSwitch struct {
	State int `json:"state"`
}

func (s *Server) switchTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024*1)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	swJSON := TaskJSONSwitch{}
	if err := json.UnmarshalRead(r.Body, &swJSON, json.RejectUnknownMembers(true)); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	userID := getUserID(r)
	task, err := s.Store.SwitchTask(r.Context(), id, userID, swJSON.State)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := json.MarshalWrite(w, task); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}

type TaskJSONBody struct {
	Body string `json:"body"`
}

func (s *Server) changeTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024*10)
	userId := getUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tbody := TaskJSONBody{}
	if err := json.UnmarshalRead(r.Body, &tbody, json.RejectUnknownMembers(true)); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	task, err := s.Store.UpdateTask(r.Context(), id, userId, tbody.Body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := json.MarshalWrite(w, task); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

}
func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024*10)
	userId := getUserID(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteTask(r.Context(), id, userId); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

type UserJSON struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type ctxKey struct{}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		JWTCookie, err := r.Cookie("jwt")
		if err != nil && !errors.Is(err, http.ErrNoCookie) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var tok string
		var ok bool
		if errors.Is(err, http.ErrNoCookie) {
			tok, ok = strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if tok == "" && JWTCookie != nil {
			tok = JWTCookie.Value
		}
		userID, err := s.parseToken(tok)
		if err != nil {
			log.Print(err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getUserID(r *http.Request) int64 { // TODO make this return error and change all functions that use it(a lot)
	idstr, _ := r.Context().Value(ctxKey{}).(string)
	id, err := strconv.ParseInt(idstr, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024*5)
	data := UserJSON{}
	if err := json.UnmarshalRead(r.Body, &data, json.RejectUnknownMembers(true)); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	_, err := s.createUser(r.Context(), data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) createUser(ctx context.Context, u UserJSON) (User, error) {
	salt := make([]byte, 8)
	if _, err := rand.Read(salt); err != nil {
		return User{}, fmt.Errorf("generating salt: %w", err)
	}
	pass := []byte(u.Password)
	pass = append(pass, salt...)
	hash, err := bcrypt.GenerateFromPassword(pass, bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("generating hash: %w", err)
	}
	user, err := s.Store.AddUser(ctx, u.Name, hash, salt)
	if err != nil {
		return User{}, fmt.Errorf("adding use to db: %w", err)
	}
	return user, nil
}

func (s *Server) authenticate(ctx context.Context, u UserJSON) (User, error) {
	user, err := s.Store.GetUserByName(ctx, u.Name)
	if err != nil {
		return User{}, fmt.Errorf("quering user: %w", err)
	}
	pass, err := s.Store.getUserPassword(ctx, user.ID)
	if err != nil {
		return User{}, fmt.Errorf("quering pass: %w", err)
	}
	password := []byte(u.Password)
	password = append(password, pass.Salt...)
	if err := bcrypt.CompareHashAndPassword(pass.Hash, password); err != nil {
		return User{}, fmt.Errorf("comparing hashes: %w", err)
	}
	return user, nil
}

func (s *Server) loginWriteToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024*5)
	user := UserJSON{}
	if err := json.UnmarshalRead(r.Body, &user, json.RejectUnknownMembers(true)); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := s.authenticate(r.Context(), user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID := strconv.FormatInt(u.ID, 10)
	tok, err := s.issueToken(userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := json.MarshalWrite(w, map[string]string{"token": tok}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
}
func (s *Server) loginSendCookie(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024*5)
	user := UserJSON{}
	if err := json.UnmarshalRead(r.Body, &user, json.RejectUnknownMembers(true)); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := s.authenticate(r.Context(), user)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID := strconv.FormatInt(u.ID, 10)
	tok, err := s.issueToken(userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	tokCookie := &http.Cookie{
		Name:     "jwt",
		Value:    tok,
		Expires:  time.Now().Add(TOKEN_EXPIRATION_TIME), // this should be the same as token
		HttpOnly: true,
		SameSite: http.SameSiteDefaultMode,
		Secure:   false, // change this with https
		Path:     "/",
	}
	http.SetCookie(w, tokCookie)
}

func (s *Server) issueToken(ID string) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   ID,
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(TOKEN_EXPIRATION_TIME)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.JWTKey)
}

func (s *Server) parseToken(str string) (string, error) {
	tok, err := jwt.ParseWithClaims(str, &jwt.RegisteredClaims{},
		func(t *jwt.Token) (any, error) { return s.JWTKey, nil },
		jwt.WithValidMethods([]string{"HS256"}))
	if err != nil {
		return "", fmt.Errorf("parsing token: %w", err)
	}
	return tok.Claims.(*jwt.RegisteredClaims).Subject, nil
}
