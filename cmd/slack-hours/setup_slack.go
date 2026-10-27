package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"charm.land/huh/v2"
)

type wizardWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Read the same workspace list used by Slack's sidebar, including hidden entries.
func listWizardWorkspaces(ctx context.Context, port int) ([]wizardWorkspace, error) {
	slack, err := connectSlack(ctx, port)
	if err != nil {
		return nil, err
	}
	defer slack.Close()
	main, _, err := slack.worlds()
	if err != nil {
		return nil, err
	}
	var workspaces []wizardWorkspace
	_, err = slack.cdp.Eval(ctx, `(() => {
   let requireModule;
   globalThis.rspackChunkGantryV2.push([['slack-hours-setup-' + Date.now()], {}, r => { requireModule = r; }]);
   const selectors = requireModule('8EYf');
   const state = requireModule('PCvj').en().getState();
   const metadata = selectors.getWorkspacesMeta(state);
   return selectors.getAllWorkspaces(state).map(id => ({id, name: metadata[id]?.name}));
 })()`, main, &workspaces)
	if err != nil {
		return nil, err
	}
	if len(workspaces) == 0 {
		return nil, errors.New("no signed-in workspaces found; sign into Slack first")
	}
	for _, workspace := range workspaces {
		if !validID.MatchString(workspace.ID) || workspace.Name == "" {
			return nil, errors.New("Slack's workspace list is not ready")
		}
	}
	return workspaces, nil
}

func chooseSlackConnection(ctx context.Context, input io.Reader, output io.Writer, port int) ([]wizardWorkspace, error) {
	attempt, cancel := context.WithTimeout(ctx, 4*time.Second)
	workspaces, err := listWizardWorkspaces(attempt, port)
	cancel()
	for err != nil {
		if ctx.Err() != nil {
			return nil, errSetupCancelled
		}
		probe, stopProbe := context.WithTimeout(ctx, 3*time.Second)
		_, _, connectionErr := discovery(probe, port)
		stopProbe()
		openSlack := connectionErr != nil && !errors.Is(connectionErr, errClientNotReady) && !errors.Is(connectionErr, errMultipleClients)
		title, description, button := "Read your workspaces", "Sign into Slack and wait for it to finish loading, then try again.\n\n"+err.Error(), "Try again"
		if errors.Is(connectionErr, errMultipleClients) {
			title, description = "Close extra Slack windows", "Keep one main Slack window open, then try again."
		} else if errors.Is(connectionErr, errClientNotReady) {
			title, description = "Sign into Slack", "Sign into your workspaces in Slack and wait for the sidebar to load, then try again."
		} else if openSlack {
			if _, statErr := os.Stat("/Applications/Slack.app"); statErr != nil {
				return nil, errors.New("install Slack at /Applications/Slack.app, sign in, then run setup again")
			}
			title, description, button = "Open Slack for setup?", "We will open Slack and read your signed-in workspaces.", "Open Slack"
			if exec.CommandContext(ctx, "/usr/bin/pgrep", "-x", "Slack").Run() == nil {
				title, description, button = "Restart Slack for setup", "Choose Slack > Quit Slack (Cmd+Q), then continue.\nClosing its window is not enough. We never quit Slack for you.", "Continue"
			}
			description += "\n\nSlack opens with a local control port. Other apps on this Mac can use it to access your signed-in Slack. Quit and open Slack normally to turn it off."
		}
		proceed := false
		form := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Title(title).Description(description).Affirmative(button).Negative("Cancel").Value(&proceed),
		).Title("slack-hours · Connect to Slack")).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).WithInput(input).WithOutput(output)
		if runErr := form.RunWithContext(ctx); runErr != nil {
			if ctx.Err() != nil || errors.Is(runErr, huh.ErrUserAborted) {
				return nil, errSetupCancelled
			}
			return nil, runErr
		}
		if !proceed {
			return nil, errSetupCancelled
		}
		attempt, cancel = context.WithTimeout(ctx, 25*time.Second)
		fmt.Fprintln(output, "Waiting for Slack…")
		err = nil
		if openSlack {
			err = startSlack(attempt, port)
		}
		if err == nil {
			for {
				workspaces, err = listWizardWorkspaces(attempt, port)
				if err == nil || attempt.Err() != nil {
					break
				}
				select {
				case <-attempt.Done():
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
		cancel()
		if err != nil {
			fmt.Fprintln(output, "Could not read your workspaces:", err)
		}
	}
	return workspaces, nil
}
