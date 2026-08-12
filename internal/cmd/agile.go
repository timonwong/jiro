package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/timonwong/jiro/internal/apperr"
	"github.com/timonwong/jiro/internal/jira"
	"github.com/timonwong/jiro/internal/output"
)

// sprintListWorkers bounds the concurrent per-Board sprint requests issued by
// sprint list, keeping the fan-out polite to the Jira Instance.
const sprintListWorkers = 4

type boardSelection struct {
	explicit bool
	value    string
}

type boardListResult struct {
	Total  int          `json:"total"`
	Boards []jira.Board `json:"boards"`
}

type sprintListResult struct {
	Total        int                  `json:"total"`
	Sprints      []jira.Sprint        `json:"sprints"`
	FailedBoards []sprintBoardFailure `json:"failedBoards,omitempty"`
}

type sprintBoardFailure struct {
	BoardID   int    `json:"boardId"`
	BoardName string `json:"boardName"`
	Error     string `json:"error"`
}

func (a *app) boardCommand() *cobra.Command {
	command := &cobra.Command{Use: "board", Short: "Inspect Jira Software Boards"}
	command.AddCommand(a.boardListCommand())
	return command
}

func (a *app) boardListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Jira Software Boards",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			boards, err := client.ListAllBoards(command.Context())
			if err != nil {
				return err
			}
			rows := make([][]string, 0, len(boards))
			for _, board := range boards {
				rows = append(rows, []string{strconv.Itoa(board.ID), board.Name, board.Type})
			}
			return a.renderDiscovery(boardListResult{Total: len(boards), Boards: boards}, output.Table{
				Columns: []output.Column{output.Fixed("ID"), output.Flexible("NAME"), output.Flexible("TYPE")},
				Rows:    rows,
			}, false)
		},
	}
}

func (a *app) sprintCommand() *cobra.Command {
	command := &cobra.Command{Use: "sprint", Short: "Inspect Jira Software Sprints"}
	command.AddCommand(a.sprintListCommand())
	return command
}

func (a *app) sprintListCommand() *cobra.Command {
	var boardSelector, stateValue string
	command := &cobra.Command{
		Use:   "list",
		Short: "List Jira Software Sprints",
		Args:  exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			state, err := jira.ParseSprintState(stateValue)
			if err != nil {
				return err
			}
			selection, err := parseBoardSelection(boardSelector, command.Flags().Changed("board"))
			if err != nil {
				return err
			}
			client, _, err := a.client()
			if err != nil {
				return err
			}
			boards, err := selection.resolveBoards(command.Context(), client)
			if err != nil {
				return err
			}
			results := fetchBoardSprints(command.Context(), client, boards, state)
			sprints := make([]jira.Sprint, 0)
			failures := make([]sprintBoardFailure, 0)
			successfulBoards := 0
			var firstErr error
			for i, board := range boards {
				listed, err := results[i].sprints, results[i].err
				sprints = append(sprints, listed...)
				if err != nil {
					if command.Context().Err() != nil {
						return command.Context().Err()
					}
					if firstErr == nil {
						firstErr = err
					}
					failures = append(failures, sprintBoardFailure{BoardID: board.ID, BoardName: board.Name, Error: err.Error()})
					if len(listed) > 0 {
						successfulBoards++
					}
					continue
				}
				successfulBoards++
			}
			result := sprintListResult{Total: len(sprints), Sprints: sprints, FailedBoards: failures}
			if len(failures) == 0 {
				return a.renderDiscovery(result, sprintTable(sprints), false)
			}
			if successfulBoards == 0 {
				return firstErr
			}
			if err := a.renderDiscovery(result, sprintTable(sprints), true); err != nil {
				return err
			}
			return apperr.New(apperr.KindPartialFailure, fmt.Sprintf("sprint list completed with %d failed Board(s)", len(failures)))
		},
	}
	command.Flags().StringVar(&boardSelector, "board", "", "Board ID or case-insensitive name substring")
	command.Flags().StringVar(&stateValue, "state", string(jira.SprintStateActive), "Sprint state: active, closed, future, or all")
	return command
}

func parseBoardSelection(selector string, explicit bool) (boardSelection, error) {
	if !explicit {
		return boardSelection{}, nil
	}
	selector = strings.TrimSpace(selector)
	if err := jira.ValidateBoardSelector(selector); err != nil {
		return boardSelection{}, err
	}
	return boardSelection{explicit: true, value: selector}, nil
}

func (s boardSelection) resolveBoards(ctx context.Context, client *jira.Client) ([]jira.Board, error) {
	if !s.explicit {
		return client.ListAllBoards(ctx)
	}
	return client.ResolveBoardSelector(ctx, s.value)
}

type sprintFetchResult struct {
	sprints []jira.Sprint
	err     error
}

// fetchBoardSprints lists every Board's Sprints through a bounded worker
// pool. Results are indexed by Board so the caller emits them strictly in
// Jira board order regardless of completion order, and the first failure in
// board order stays the first failure the caller sees (ADR-0005, ADR-0013).
// Workers write only their own index; app state stays with the caller.
func fetchBoardSprints(ctx context.Context, client *jira.Client, boards []jira.Board, state jira.SprintState) []sprintFetchResult {
	results := make([]sprintFetchResult, len(boards))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(sprintListWorkers, len(boards)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					results[i] = sprintFetchResult{err: ctx.Err()}
					continue
				}
				listed, err := client.ListAllSprints(ctx, boards[i].ID, state)
				for j := range listed {
					listed[j].BoardName = boards[i].Name
				}
				results[i] = sprintFetchResult{sprints: listed, err: err}
			}
		}()
	}
	for i := range boards {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func sprintTable(sprints []jira.Sprint) output.Table {
	rows := make([][]string, 0, len(sprints))
	for _, sprint := range sprints {
		rows = append(rows, []string{
			strconv.Itoa(sprint.ID), sprint.Name, sprint.State, strconv.Itoa(sprint.BoardID), sprint.BoardName,
			sprint.StartDate, sprint.EndDate, sprint.CompleteDate,
		})
	}
	return output.Table{
		Columns: []output.Column{
			output.Fixed("ID"), output.Flexible("NAME"), output.Fixed("STATE"), output.Fixed("BOARD ID"),
			output.Flexible("BOARD NAME"), output.Flexible("START"), output.Flexible("END"), output.Flexible("COMPLETE"),
		},
		Rows: rows,
	}
}

func (a *app) renderDiscovery(data any, table output.Table, partial bool) error {
	renderer, err := a.renderer()
	if err != nil {
		return err
	}
	if partial {
		renderer.Quiet = false
	}
	if renderer.Format == output.FormatText {
		if len(table.Rows) == 0 {
			return nil
		}
		return renderer.Success(table)
	}
	return renderer.Success(data)
}
