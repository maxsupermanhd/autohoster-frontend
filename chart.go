package main

import (
	"fmt"
	"hash/crc32"
	"html/template"
)

var (
	chartSCcolorLost    = "#c33c"
	chartSCcolorWon     = "#3c3c"
	chartSCcolorNeutral = "#17fc"
)

type primitiveStackedChart struct {
	ID          string
	Caption     string
	AxisY       string
	AxisX       string
	Data        []primitiveStackedChartColumn
	MaxValue    int
	Orientation string
	Width       string
	LabelWidth  string
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

func newSCVertical(caption, axisX, axisY string) *primitiveStackedChart {
	return &primitiveStackedChart{
		ID:          fmt.Sprint(crc32.Checksum([]byte(caption), crc32.IEEETable)),
		Orientation: "column",
		Caption:     caption,
		AxisY:       axisY,
		AxisX:       axisX,
		Data:        []primitiveStackedChartColumn{},
		MaxValue:    0,
		Width:       "4ex",
	}
}

func newSCHorizontal(caption, axisX, axisY string) *primitiveStackedChart {
	return &primitiveStackedChart{
		ID:          fmt.Sprint(crc32.Checksum([]byte(caption), crc32.IEEETable)),
		Orientation: "bar",
		Caption:     caption,
		AxisY:       axisY,
		AxisX:       axisX,
		Data:        []primitiveStackedChartColumn{},
		MaxValue:    0,
		Width:       "unset",
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
