package actions

import (
	"fmt"
	"strings"
	"time"

	"github.com/aclemen1/due-cli/internal/connect"
	"github.com/aclemen1/due-cli/internal/ledger"
	"github.com/aclemen1/due-cli/internal/spec"
)

// findLine finds a connector line of the sphere by its id (with or without its
// occurrence), and its source when several connectors could hold it.
func findLine(ctx *spec.Context, l *ledger.Ledger, id, source string, acked bool) (connect.Item, error) {
	cfg, _, _ := Open(ctx)
	res, err := List(cfg, l, Query{Until: "3650d", All: true})
	if err != nil {
		return connect.Item{}, err
	}
	var found []connect.Item
	for _, it := range res.Items {
		if it.Type == "due" || (source != "" && it.Source != source) {
			continue
		}
		if it.ID != id && ledger.BaseID(it.ID) != id {
			continue
		}
		if acked != (it.State == "acked") {
			continue
		}
		found = append(found, it)
	}
	switch {
	case len(found) == 0 && strings.Contains(strings.ToUpper(id), "E-"):
		return connect.Item{}, spec.UserError("%s is an entry of the ledger: use due done %s", id, id)
	case len(found) == 0:
		return connect.Item{}, spec.NotFound("no line %s in %s; find its id with: due ls --format json", id, l.Sphere)
	case len(found) > 1:
		var srcs []string
		for _, it := range found {
			srcs = append(srcs, it.Source)
		}
		return connect.Item{}, spec.UserError("%s is in several sources (%s): give --source", id, strings.Join(srcs, ", "))
	}
	return found[0], nil
}

func registerAck() {
	params := func(extra ...spec.Param) []spec.Param {
		return append([]spec.Param{
			{Name: "id", Kind: spec.String, Positional: true, Required: true, Help: "Id of the line, as due ls gives it, e.g. 01M44DR8KNBNK912RHEY2FBQS3#0."},
			{Name: "source", Kind: spec.String, Help: "Connector of the line, e.g. mnemo; needed only when two hold the same id."},
		}, append(extra, writeSphereParam())...)
	}
	spec.Register(&spec.Action{
		Category: "due", Name: "ack", Top: true,
		Summary: "Take a line of another tool off due (seen and settled), for its date: a late date of the memory, a deadline met.",
		Discussion: "For lines of connectors; an entry of the ledger is closed with due done. The line leaves due ls and the alerts; " +
			"if its source moves it to another date, it comes back. due ls --all shows it, as acked; due unack gives it back.",
		Params:   params(spec.Param{Name: "note", Kind: spec.String, Help: "What settled it, kept with the acknowledgement."}),
		Effects:  []string{"Records the acknowledgement in the sphere's ledger (acks.yaml) and commits."},
		Examples: []string{"due ack 01M44DR8KNBNK912RHEY2FBQS3#0 --sphere perso", "due ack 01M45CW9NVW0VGJXJT2WZKFBSY#0 --note \"DI déposée le 28.09\" --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			_, l, err := Open(ctx)
			if err != nil {
				return nil, err
			}
			it, err := findLine(ctx, l, strings.TrimSpace(ctx.Str("id")), ctx.Str("source"), false)
			if err != nil {
				return nil, err
			}
			err = l.WriteAs(func() (string, error) {
				acks, err := l.Acks()
				if err != nil {
					return "", err
				}
				acks = append(acks, ledger.Ack{Source: it.Source, ID: ledger.BaseID(it.ID), At: it.At.Format(time.RFC3339),
					Title: it.Title, By: l.By, When: l.Now().Format(time.RFC3339), Note: strings.TrimSpace(ctx.Str("note"))})
				if err := l.SaveAcks(acks); err != nil {
					return "", err
				}
				return fmt.Sprintf("ack %s %s: %s", it.Source, it.ID, it.Title), nil
			})
			if err != nil {
				return nil, err
			}
			it.State = "acked"
			return it, nil
		},
	})
	spec.Register(&spec.Action{
		Category: "due", Name: "unack", Top: true,
		Summary:  "Give back a line taken off due with due ack.",
		Params:   params(),
		Effects:  []string{"Removes the acknowledgement from acks.yaml and commits."},
		Examples: []string{"due unack 01M44DR8KNBNK912RHEY2FBQS3#0 --sphere perso"},
		Run: func(ctx *spec.Context) (any, error) {
			_, l, err := Open(ctx)
			if err != nil {
				return nil, err
			}
			it, err := findLine(ctx, l, strings.TrimSpace(ctx.Str("id")), ctx.Str("source"), true)
			if err != nil {
				return nil, err
			}
			err = l.WriteAs(func() (string, error) {
				acks, err := l.Acks()
				if err != nil {
					return "", err
				}
				key := ledger.AckKey(it.Source, it.ID, it.At.Format(time.RFC3339))
				var kept []ledger.Ack
				for _, a := range acks {
					if ledger.AckKey(a.Source, a.ID, a.At) != key {
						kept = append(kept, a)
					}
				}
				if err := l.SaveAcks(kept); err != nil {
					return "", err
				}
				return fmt.Sprintf("unack %s %s: %s", it.Source, it.ID, it.Title), nil
			})
			if err != nil {
				return nil, err
			}
			it.State = ""
			return it, nil
		},
	})
}
