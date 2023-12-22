package main

import (
	"log"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/rivo/tview"
)

func (s *Commander) ShowUI() {
	app := tview.NewApplication()
	table := tview.NewTable()
	table.SetBorders(true)

	var meetings []model.Meeting
	from := time.Now().AddDate(0, 0, -1*7)
	to := time.Now().AddDate(0, 0, 0)

	m, err := s.client.GetIntervalMeetings(from, to)
	if err != nil {
		log.Printf("[ERROR] GetIntervalMeetings: %e", err)
	}
	meetings = append(meetings, m...)

	table.SetCell(0, 0,
		tview.NewTableCell("Topic").
			SetTextColor(tcell.ColorYellow).
			// SetBackgroundColor(tcell.ColorSkyblue).
			SetAlign(tview.AlignLeft))
	table.SetCell(0, 1,
		tview.NewTableCell("UUID").
			SetTextColor(tcell.ColorYellow).
			SetAlign(tview.AlignCenter))
	table.SetCell(0, 2,
		tview.NewTableCell("StartTime").
			SetTextColor(tcell.ColorYellow).
			SetAlign(tview.AlignCenter))
	table.SetCell(0, 3,
		tview.NewTableCell("Size").
			SetTextColor(tcell.ColorYellow).
			SetAlign(tview.AlignLeft))

	var totalSize model.FileSize

	for i, m := range meetings {

		var meetingSize model.FileSize
		for _, r := range m.Records {
			meetingSize += r.FileSize
			totalSize += r.FileSize
			// log.Printf("[DEBUG] record %d: %+v", j, r)
		}
		table.SetCell(i+1, 0,
			tview.NewTableCell(m.Topic).
				SetTextColor(tcell.ColorWhite).
				SetAlign(tview.AlignLeft))

		table.SetCell(i+1, 1,
			tview.NewTableCell(m.UUID).
				SetTextColor(tcell.ColorWhite).
				SetAlign(tview.AlignCenter))

		table.SetCell(i+1, 2,
			tview.NewTableCell(m.StartTime.Local().Format("2006-01-02 15:04:05")).
				SetTextColor(tcell.ColorDarkCyan).
				SetAlign(tview.AlignCenter))

		table.SetCell(i+1, 3,
			tview.NewTableCell(meetingSize.String()).
				SetTextColor(tcell.ColorDarkCyan).
				SetAlign(tview.AlignLeft))

	}

	table.Select(0, 0).SetFixed(1, 1).SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			app.Stop()
		}
		if key == tcell.KeyEnter {
			table.SetSelectable(true, false)
		}
	}).SetSelectedFunc(func(row int, column int) {
		// table.GetCell(row, column).SetTextColor(tcell.ColorRed)
		table.SetSelectable(false, false)
	})

	if err := app.SetRoot(table, true).EnableMouse(true).Run(); err != nil {
		panic(err)
	}

	log.Printf("[INFO] total size: %s", totalSize.String())

}
