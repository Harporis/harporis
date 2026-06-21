package main

import (
	"testing"

	v1 "github.com/Harporis/harporis/contracts/gen/go/harporis/v1"
	"github.com/Harporis/harporis/services/getter/internal/git"
)

func TestSourceFromProto_Basic(t *testing.T) {
	s := &v1.Source{Src: &v1.Source_Remote{Remote: &v1.RemoteRepo{
		Url:  "https://example.com/r.git",
		Auth: &v1.RemoteRepo_Basic{Basic: &v1.BasicAuth{Username: "alice", Password: "pw"}},
	}}}
	got, err := sourceFromProto(s)
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
	got, err := sourceFromProto(s)
	if err != nil {
		t.Fatalf("sourceFromProto: %v", err)
	}
	rs := got.(git.RemoteSource)
	if rs.Header.Name != "Authorization" || rs.Header.Value != "Bearer x" {
		t.Fatalf("header not mapped: %+v", rs)
	}
}
