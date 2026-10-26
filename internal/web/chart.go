package web

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/candylario/app/internal/engine"
)

// chartView is an inline SVG of an agent's equity against buying and
// holding, as path data.
type chartView struct {
	Points     int
	Width      int
	Height     int
	Equity     string
	BuyAndHold string
	Min        string
	Max        string
	From       string
	To         string
	Last       string
	LastBench  string
	Up         bool
}

const (
	chartWidth  = 640
	chartHeight = 200
	chartPad    = 6
)

func buildChart(points []engine.Point) chartView {
	v := chartView{Points: len(points), Width: chartWidth, Height: chartHeight}
	if len(points) < 2 {
		return v
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range points {
		lo = math.Min(lo, math.Min(p.Equity, p.BuyAndHold))
		hi = math.Max(hi, math.Max(p.Equity, p.BuyAndHold))
	}
	if hi-lo < 1e-9 {
		hi, lo = hi+1, lo-1
	}
	span := hi - lo
	lo, hi = lo-span*0.05, hi+span*0.05
	first, last := points[0].At, points[len(points)-1].At
	dur := last.Sub(first).Seconds()
	if dur <= 0 {
		dur = 1
	}
	x := func(t time.Time) float64 {
		return chartPad + (chartWidth-2*chartPad)*t.Sub(first).Seconds()/dur
	}
	y := func(val float64) float64 {
		return chartPad + (chartHeight-2*chartPad)*(1-(val-lo)/(hi-lo))
	}
	var eq, bh strings.Builder
	for i, p := range points {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&eq, "%s%.1f,%.1f ", cmd, x(p.At), y(p.Equity))
		fmt.Fprintf(&bh, "%s%.1f,%.1f ", cmd, x(p.At), y(p.BuyAndHold))
	}
	end := points[len(points)-1]
	v.Equity, v.BuyAndHold = strings.TrimSpace(eq.String()), strings.TrimSpace(bh.String())
	v.Min, v.Max = fmt.Sprintf("%.2f", lo+span*0.05), fmt.Sprintf("%.2f", hi-span*0.05)
	v.From, v.To = first.UTC().Format("Jan 2"), last.UTC().Format("Jan 2 15:04")
	v.Last, v.LastBench = fmt.Sprintf("%.2f", end.Equity), fmt.Sprintf("%.2f", end.BuyAndHold)
	v.Up = end.Equity >= points[0].Equity
	return v
}
