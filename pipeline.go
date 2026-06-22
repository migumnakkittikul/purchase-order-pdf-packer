package main

import (
	"math"
	"strconv"
	"strings"
)

// labelsNeeded - the label policy:
// if the order unit has a parenthesised pack size (e.g. ลัง(6)), print one
// label per ordered unit (จำนวน); otherwise print one label for the line.
// Example: จำนวน 10, unit ลัง(6) -> 10 labels.
func labelsNeeded(it Item) int {
	if !it.Packed {
		return 1
	}
	q, ok := parseNum(it.Qty)
	if !ok {
		return 1
	}
	n := int(math.Ceil(q))
	if n < 1 {
		n = 1
	}
	return n
}

// ---- number formatting ---------------------------------------------------- //

func addCommas(intPart string) string {
	neg := strings.HasPrefix(intPart, "-")
	intPart = strings.TrimPrefix(intPart, "-")
	n := len(intPart)
	if n <= 3 {
		if neg {
			return "-" + intPart
		}
		return intPart
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(intPart[:pre])
	}
	for i := pre; i < n; i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(intPart[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func fmtNum(v float64, dec int) string {
	s := strconv.FormatFloat(v, 'f', dec, 64)
	if dec > 0 {
		parts := strings.SplitN(s, ".", 2)
		return addCommas(parts[0]) + "." + parts[1]
	}
	return addCommas(s)
}

func fmtQty(it Item) string {
	if v, ok := parseNum(it.Qty); ok {
		return fmtNum(v, 0)
	}
	return it.Qty
}

func fmtTotal(it Item) string {
	if v, ok := parseNum(it.Qty); ok {
		return fmtNum(v*float64(it.Pack), 0)
	}
	return ""
}

func fmtMoney(price string) string {
	if v, ok := parseNum(price); ok {
		return fmtNum(v, 2)
	}
	return price
}
