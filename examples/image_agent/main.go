// This example lets the model call Ark image generation and return image URLs.
// Set DEEPSEEK_API_KEY and ARK_API_KEY, and optionally DEEPSEEK_MODEL or ARK_IMAGE_MODEL, then run:
// go run ./examples/image_agent "Generate a transparent embroidered sticker"
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/herlanai/omnix/pkg/agent"
	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/credentials"
	"github.com/herlanai/omnix/pkg/imagegen/provider_ark"
	"github.com/herlanai/omnix/pkg/llm/provider_deepseek"
	"github.com/herlanai/omnix/pkg/session"
	"github.com/herlanai/omnix/pkg/sysprompt"
	"github.com/herlanai/omnix/pkg/tools"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		return fmt.Errorf("set DEEPSEEK_API_KEY before running this example")
	}
	question := strings.TrimSpace(strings.Join(os.Args[1:], " "))
	if question == "" {
		return fmt.Errorf("usage: go run ./examples/image_agent <question>")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	store := credentials.NewMemoryStore()
	if err := store.Set(ctx, brand.NewCredentialRef("DEEPSEEK_API_KEY"), key); err != nil {
		return err
	}
	provider := provider_deepseek.NewDeepSeek(store)
	defer provider.HTTPClient().CloseIdleConnections()
	imageKey := os.Getenv("ARK_API_KEY")
	if imageKey == "" {
		return fmt.Errorf("set ARK_API_KEY before running this example")
	}
	if err := store.Set(ctx, brand.NewCredentialRef("ARK_API_KEY"), imageKey); err != nil {
		return err
	}
	var options []provider_ark.Option
	if model := os.Getenv("ARK_IMAGE_MODEL"); model != "" {
		options = append(options, provider_ark.WithModel(model))
	}
	images := provider_ark.New(store, options...)
	pipeline := tools.NewPipeline().WithTool(images.Tool())
	prompt := sysprompt.New()
	prompt.Register("image-generation", 100, "Use ark_generate_image when the user requests image creation. Preserve requested text, style and composition. Select transparent background only when requested, and use PNG for transparency. Return generated image URLs using Markdown image syntax. Do not fabricate URLs or claim success if the tool fails. Do not repeat a failed generation unchanged. Answer in the user's language.")
	id := brand.NewSessionID("image-agent")
	log := session.NewSessionLog(id)
	a := agent.NewAgent(id, log, prompt, pipeline, provider, agent.AgentOptions{Model: os.Getenv("DEEPSEEK_MODEL")})
	defer a.Dispose()
	a.SetRunContext(ctx, 5*time.Minute)
	if err := a.Run(question); err != nil {
		return err
	}
	if err := a.WhenIdle(ctx); err != nil {
		a.Cancel(agent.CancelUser)
		return err
	}
	var executionErr error
	for _, event := range log.Events() {
		switch data := event.Data.(type) {
		case session.AssistantMessageData:
			if data.Content != "" {
				fmt.Println(data.Content)
			}
		case session.ToolCallData:
			fmt.Fprintf(os.Stderr, "Tool: %s\n", data.Tool)
		case session.ToolResultData:
			if data.IsError {
				fmt.Fprintf(os.Stderr, "Tool error: %s\n", data.Output)
			}
		case session.AgentErrorData:
			executionErr = fmt.Errorf("agent execution failed: %v", data)
		}
	}
	return executionErr
}
