package main

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/getter/internal/config"
	"github.com/Harporis/harporis/services/getter/internal/git"
)

func TestSourceFromProto_Basic(t *testing.T) {
	s := &v1.Source{Src: &v1.Source_Remote{Remote: &v1.RemoteRepo{
		Url:  "https://example.com/r.git",
		Auth: &v1.RemoteRepo_Basic{Basic: &v1.BasicAuth{Username: "alice", Password: "pw"}},
	}}}
	got, err := sourceFromProto(s, nil)
	if err != nil {
		t.Fatalf("sourceFromProto: %v", err)
	}
	rs, ok := got.(git.RemoteSource)
	if !ok {
		t.Fatalf("want RemoteSource, got %T", got)
	}
	if rs.BasicUser != "alice" || rs.BasicPassword != "pw" {
		t.Fatalf("basic not mapped: %+v", rs)
	}
}

func TestSourceFromProto_Header(t *testing.T) {
	s := &v1.Source{Src: &v1.Source_Remote{Remote: &v1.RemoteRepo{
		Url:  "https://example.com/r.git",
		Auth: &v1.RemoteRepo_Header{Header: &v1.HeaderAuth{Name: "Authorization", Value: "Bearer x"}},
	}}}
	got, err := sourceFromProto(s, nil)
	if err != nil {
		t.Fatalf("sourceFromProto: %v", err)
	}
	rs := got.(git.RemoteSource)
	if rs.Header.Name != "Authorization" || rs.Header.Value != "Bearer x" {
		t.Fatalf("header not mapped: %+v", rs)
	}
}

func TestResolveAuth_Precedence(t *testing.T) {
	defs := []config.HostAuth{
		{Host: "gitlab.mycompany.com", Header: &config.HeaderAuthCfg{Name: "PRIVATE-TOKEN", Value: "glt"}},
		{Host: "github.com", Token: "ght"},
	}
	t.Run("per-scan auth wins (defaults ignored)", func(t *testing.T) {
		tok, _, _, _, _ := resolveAuth("https://github.com/x.git", true, defs)
		if tok != "" {
			t.Fatalf("expected no default applied, got token %q", tok)
		}
	})
	t.Run("exact host match applies token", func(t *testing.T) {
		tok, _, _, _, _ := resolveAuth("https://github.com/x.git", false, defs)
		if tok != "ght" {
			t.Fatalf("token = %q, want ght", tok)
		}
	})
	t.Run("header default applies", func(t *testing.T) {
		_, _, _, hn, hv := resolveAuth("https://gitlab.mycompany.com/x.git", false, defs)
		if hn != "PRIVATE-TOKEN" || hv != "glt" {
			t.Fatalf("header = %q:%q", hn, hv)
		}
	})
	t.Run("dot-suffix host match", func(t *testing.T) {
		defs2 := []config.HostAuth{{Host: ".example.com", Token: "et"}}
		tok, _, _, _, _ := resolveAuth("https://git.example.com/x.git", false, defs2)
		if tok != "et" {
			t.Fatalf("suffix match failed, token=%q", tok)
		}
	})
	t.Run("no match → no auth", func(t *testing.T) {
		tok, _, _, _, _ := resolveAuth("https://other.com/x.git", false, defs)
		if tok != "" {
			t.Fatalf("expected empty, got %q", tok)
		}
	})
}
