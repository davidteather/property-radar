package streeteasy

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/davidteather/property-radar/internal/domain"
)

//go:embed areas.json
var areasJSON []byte

type wireArea struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Short    string `json:"short"`
	Level    int    `json:"level"`
	ParentID int    `json:"parentId"`
	Borough  string `json:"borough"`
}

var (
	areasOnce  sync.Once
	areasCache []domain.Area
	areasErr   error
)

// Areas returns the embedded StreetEasy area taxonomy as domain.Area values.
func Areas() ([]domain.Area, error) {
	areasOnce.Do(func() {
		var wire []wireArea
		if err := json.Unmarshal(areasJSON, &wire); err != nil {
			areasErr = fmt.Errorf("parse embedded areas: %w", err)
			return
		}
		out := make([]domain.Area, 0, len(wire))
		for _, w := range wire {
			parent := ""
			if w.ParentID != 0 {
				parent = strconv.Itoa(w.ParentID)
			}
			out = append(out, domain.Area{
				Provider: ProviderName, ID: w.ID, Name: w.Name, Short: w.Short,
				Borough: w.Borough, Level: w.Level, ParentID: parent,
			})
		}
		areasCache = out
	})
	return areasCache, areasErr
}
