package server

// Starting a session on a repository: turning the "owner/name" a client picked
// into something that can clone it.
//
// Separate from the endpoints that connect an account because it answers a
// different question. Those are about a credential — get one, keep it, renew
// it. This is about a session, and it runs on the create path rather than on
// any /api/forges route.

import (
	"context"
	"fmt"
	"strings"

	"github.com/dougkirkley/dross/internal/forge"
)

// forgeClone resolves the repository a new session asked for into something
// that can clone it.
//
// The clone URL comes from the host's own listing rather than from the request.
// A client that could name the address would be naming where this server sends
// the user's token: "clone https://evil.test/x" would be answered with a URL
// containing the credential, and even unused it would be written into a Secret
// and handed to a pod. Matching a full name against what the account can
// actually see costs one API call and removes the question.
func (s *Server) forgeClone(ctx context.Context, owner string, req CreateReq) (*CloneSpec, error) {
	if req.Forge == "" {
		return nil, fmt.Errorf("%w: %s names no git host to look it up on", errNoSuchRepo, req.Repo)
	}
	f, ok := forge.Get(req.Forge)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a configured git host", errNoSuchRepo, req.Forge)
	}
	if owner == "" {
		return nil, fmt.Errorf("%w: a repository needs a signed-in account", errNotConnected)
	}
	tok, err := s.forgeToken(ctx, owner, f)
	if err != nil {
		return nil, err
	}
	repos, err := f.Repos(ctx, tok.Access)
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		if !strings.EqualFold(repo.FullName, req.Repo) {
			continue
		}
		branch := req.Branch
		if branch == "" {
			branch = repo.Branch
		}
		return &CloneSpec{
			FullName:     repo.FullName,
			URL:          repo.CloneURL,
			Credentialed: f.CloneURL(repo.CloneURL, tok.Access),
			Branch:       branch,
		}, nil
	}
	return nil, fmt.Errorf("%w: %s is not one of the repositories %s can see",
		errNoSuchRepo, req.Repo, f.Name())
}

// cloneRepo puts a repository on disk. A variable so a test can watch what it
// was asked for without running git; the real one is covered where it lives.
var cloneRepo = forge.Clone

// branchRepo cuts a session's working branch. A variable for the same reason as
// cloneRepo.
var branchRepo = forge.Branch

// CloneSpec is the repository a session starts on.
type CloneSpec struct {
	// FullName is "owner/name", which is what the session is labelled with.
	FullName string
	// URL clones nothing on its own. It is the plain address, kept apart from
	// the credentialed one so the two are never confused at a call site: this
	// is the one that can be stored, logged and shown.
	URL string
	// Credentialed is the same URL with the token in it. It goes into a Secret
	// and nowhere else.
	Credentialed string
	// Branch is what to check out, already defaulted to the repository's own.
	Branch string
}

// WorkBranch is the branch a session makes and works on.
//
// Prefixed, so a repository full of these says at a glance which branches came
// from dross and which a person made. The name is the session's own, which is
// unique already and is what the session is called everywhere else — so a
// branch, a pull request and a session card all say the same word.
//
// git refuses plenty of things in a ref name and the session name is generated
// from a word list, so this is a narrowing rather than a sanitiser: what
// survives is lowercase, digits and dashes, and a name that survives as nothing
// falls back to something that is at least a valid ref.
func WorkBranch(session string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 32
		case r == '-', r == '_':
			return '-'
		}
		return -1
	}, session)
	name = strings.Trim(name, "-.")
	if name == "" {
		name = "session"
	}
	return "dross/" + name
}
