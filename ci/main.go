package main

import (
	"context"
	"fmt"
)

const (
	REPO = "github.com/kpenfound/greetings-api"
	IMAGE = "kylepenfound/greetings-api:latest"
)

type Greetings struct{}

func (g *Greetings) UnitTest(ctx context.Context, dir *Directory) (string, error) {
	backendResult, err := dag.Backend().UnitTest(ctx, dir)
	if err != nil {
		return "", err
	}

	return backendResult, nil
}

func (g *Greetings) Lint(ctx context.Context, dir *Directory) (string, error) {
	backendResult, err := dag.Backend().Lint(ctx, dir)
	if err != nil {
		return "", err
	}
	return backendResult, nil
}

func (g *Greetings) Build(dir *Directory, env string) *Directory {
	return dag.Directory().
		WithFile("/build/greetings-api", dag.Backend().Binary(dir)).
		WithDirectory("build/website/", dag.Frontend().Build(dir.Directory("website"), FrontendBuildOpts{Env: env}))
}

func (g *Greetings) Serve(dir *Directory) *Service {
	backendService := dag.Backend().Serve(dir)
	frontendService := dag.Frontend().Serve(dir.Directory("website"))

	return dag.Proxy().
		WithService(backendService, "backend", 8080, 8080).
		WithService(frontendService, "frontend", 8081, 80).
		Service()
}

func (g *Greetings) Release(ctx context.Context, dir *Directory, tag string, ghToken *Secret) (string, error) {
	// Get build
	build := g.Build(dir, "netlify")
	// Compress frontend build
	assets := dag.Container().From("alpine:3.18").
		WithDirectory("/assets", build).
		WithWorkdir("/assets/build").
		WithExec([]string{"tar", "czf", "website.tar.gz", "website/"}).
		WithExec([]string{"rm", "-r", "website"}).
		Directory("/assets/build")

	title := fmt.Sprintf("Release %s", tag)

	return dag.GithubRelease().Create(ctx, REPO, tag, title, ghToken, GithubReleaseCreateOpts{Assets: assets})
}

func (g *Greetings) Deploy(ctx context.Context, dir *Directory, flyToken *Secret, netlifyToken *Secret, registryUser string, registryPass *Secret) (string, error) {
	// Backend
	backendAmd64 := dag.Backend().Container(dir, BackendContainerOpts{Arch: "amd64"})
	backendArm64 := dag.Backend().Container(dir, BackendContainerOpts{Arch: "arm64"})
	_, err := dag.Container().
		WithRegistryAuth(
			"index.docker.io",
			registryUser,
			registryPass,
		).
		Publish(ctx, IMAGE, ContainerPublishOpts{
			PlatformVariants: []*Container{
				backendAmd64,
				backendArm64,
			},
	})
	if err != nil {
		return "", err
	}
	backendResult, err := fly_deploy(ctx, IMAGE, flyToken) // Pass tag. Fly isn't happy with full shas
	if err != nil {
		return "", err
	}
	// Frontend
	frontend := dag.Frontend().Build(dir.Directory("website"), FrontendBuildOpts{Env: "netlify"})
	frontendResult, err := netlify_deploy(ctx, frontend, netlifyToken)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("BACKEND\n\n%s\n\nFRONTEND\n\n%s", backendResult, frontendResult), nil
}

func (g *Greetings) Ci(
	ctx context.Context,
	dir *Directory,
	release Optional[bool],
	tag Optional[string],
	infisicalToken Optional[*Secret],
) (string, error) {
	out, err := g.Lint(ctx, dir)
	if err != nil {
		return "", err
	}
	testOut, err := g.UnitTest(ctx, dir)
	if err != nil {
		return "", err
	}
	out = out + "\n" + testOut

	infisical, isset := infisicalToken.Get()

	if release.GetOr(false) && isset {
		tag_, tagSet := tag.Get()
		ghToken := dag.Infisical().GetSecret("GH_RELEASE_TOKEN", infisical, "dev", "/")

		if tagSet {
			releaseOut, err := g.Release(ctx, dir, tag_, ghToken)
			if err != nil {
				return "", err
			}
			out = out + "\n" + releaseOut
		}

		flyToken := dag.Infisical().GetSecret("FLY_TOKEN", infisical, "dev", "/")
		netlifyToken := dag.Infisical().GetSecret("NETLIFY_TOKEN", infisical, "dev", "/")
		registryUser, err := dag.Infisical().GetSecret("DOCKERHUB_USER", infisical, "dev", "/").Plaintext(ctx)
		if err != nil {
			return "", err
		}
		registryPass := dag.Infisical().GetSecret("DOCKERHUB_PASSWORD", infisical, "dev", "/")

		deployOut, err := g.Deploy(ctx, dir, flyToken, netlifyToken, registryUser, registryPass)
		if err != nil {
			return "", err
		}
		out = out + "\n" + deployOut
	}

	return out, nil
}

func (g *Greetings) CiRemote(
	ctx context.Context,
	commit string,
	release Optional[bool],
	tag Optional[string],
	infisicalToken Optional[*Secret],
) (string, error) {
	dir := dag.Git(fmt.Sprintf("https://%s", REPO)).
		Commit(commit).
		Tree()

	return g.Ci(
		ctx,
		dir,
		release,
		tag,
		infisicalToken,
	)
}
func fly_deploy(ctx context.Context, imageRef string, token *Secret) (string, error) {
	app := "dagger-demo"
	out, err := dag.Fly().Deploy(ctx, app, imageRef, token)
	return out, err
}

func netlify_deploy(ctx context.Context, dir *Directory, token *Secret) (string, error) {
	site := "dagger-demo"
	out, err := dag.Netlify().Deploy(ctx, dir, token, site)
	return out, err
}
