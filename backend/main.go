package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"cascade-demoops/backend/internal/agents"
	"cascade-demoops/backend/internal/model"
	"cascade-demoops/backend/internal/orchestrator"
)

func main() {
	mode := flag.String("mode", string(model.AppModeDesktop), "runtime mode: web or desktop")
	productURL := flag.String("product-url", "", "staging/local product URL to explore")
	gitRepoURL := flag.String("git-repo-url", "", "Git repository URL for web mode")
	localRepoPath := flag.String("local-repo-path", "", "absolute local repository path for desktop mode")
	targetAudience := flag.String("target-audience", "seed investor", "demo target audience")
	productDescription := flag.String("product-description", "", "short product description")
	autoApprove := flag.Bool("auto-approve", false, "continue after graph generation without frontend review; dev only")
	flag.Parse()

	input := orchestrator.UserInput{
		Mode:               model.AppMode(*mode),
		ProductURL:         *productURL,
		GitRepoURL:         *gitRepoURL,
		LocalRepoPath:      *localRepoPath,
		ProductDescription: *productDescription,
		TargetAudience:     *targetAudience,
	}

	flow, err := orchestrator.NewCascadeFlow(orchestrator.Dependencies{
		InputContext:        agents.NewInputContextAgent(),
		RequirementReader:   agents.NewRequirementReaderAgent(),
		CodeReader:          agents.NewCodeReaderAgent(),
		PageReader:          agents.NewPageReaderAgent(),
		ProjectIntelligence: agents.NewProjectIntelligenceGraph(),
		Understanding:       agents.NewMultimodalUnderstandingAgent(),
		ProductMap:          agents.NewProductMapAgent(),
		GraphBuilder:        agents.NewGraphBuilderAgent(),
		ScriptPackager:      agents.NewScriptPackagerAgent(),
		QAExecutor:          agents.NewQAExecutorAgent(),
		AssetGenerator:      agents.NewAssetGeneratorAgent(),
	})
	must(err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	state, err := flow.Start(ctx, input)
	must(err)

	if *autoApprove {
		state, err = flow.ApproveAndContinue(ctx, state, state.WorkflowGraph)
		must(err)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	must(encoder.Encode(state))

	if state.Status == orchestrator.FlowStatusAwaitingHuman {
		fmt.Fprintln(os.Stderr, "workflow graph and script document generated; waiting for HumanApprove")
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
