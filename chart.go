package main

import "html/template"

var (
	chartSCcolorLost    = "#c33c"
	chartSCcolorWon     = "#3c3c"
	chartSCcolorNeutral = "#17fc"
)

type primitiveStackedChart struct {
	Caption  string
	AxisY    string
	AxisX    string
	Data     []primitiveStackedChartColumn
	MaxValue int
}

type primitiveStackedChartColumn struct {
	Label  template.HTML
	Values []primitiveStackedChartColumnValue
}

type primitiveStackedChartColumnValue struct {
	Label string
	Color string
	Value int
}

func newSC(caption, axisX, axisY string) *primitiveStackedChart {
	return &primitiveStackedChart{
		Caption:  caption,
		AxisY:    axisY,
		AxisX:    axisX,
		Data:     []primitiveStackedChartColumn{},
		MaxValue: 0,
	}
}

func (ch *primitiveStackedChart) calcTotals() *primitiveStackedChart {
	ch.MaxValue = 0
	for _, v := range ch.Data {
		columnSum := 0
		for _, vv := range v.Values {
			columnSum += vv.Value
		}
		if columnSum > ch.MaxValue {
			ch.MaxValue = columnSum
		}
	}
	return ch
}

func (ch *primitiveStackedChart) appendToColumn(colname, label, color string, value int) {
	for i, v := range ch.Data {
		if v.Label == template.HTML(colname) {
			ch.Data[i].Values = append(ch.Data[i].Values, primitiveStackedChartColumnValue{
				Label: label,
				Color: color,
				Value: value,
			})
			return
		}
	}
	ch.Data = append(ch.Data, primitiveStackedChartColumn{
		Label: template.HTML(colname),
		Values: []primitiveStackedChartColumnValue{primitiveStackedChartColumnValue{
			Label: label,
			Color: color,
			Value: value,
		}},
	})
}
