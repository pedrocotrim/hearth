package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const maxUnits int64 = 10_000_000

func Currencies() []string {
	return []string{"USD", "EUR", "GBP", "BRL", "CAD", "AUD"}
}

func Valid(code string) bool {
	switch code {
	case "USD", "EUR", "GBP", "BRL", "CAD", "AUD":
		return true
	default:
		return false
	}
}

func Format(cents int64, currency string) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%s%d.%02d", sign, symbol(currency), cents/100, cents%100)
}

func Input(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func Parse(input string) (int64, error) {
	s, err := normalize(input)
	if err != nil {
		return 0, err
	}
	return parseNormalized(s)
}

func ParseOptional(input string) (int64, error) {
	if strings.TrimSpace(input) == "" {
		return 0, nil
	}
	return Parse(input)
}

func symbol(currency string) string {
	switch currency {
	case "USD":
		return "$"
	case "EUR":
		return "€"
	case "GBP":
		return "£"
	case "BRL":
		return "R$"
	case "CAD":
		return "CA$"
	case "AUD":
		return "A$"
	default:
		if currency == "" {
			return "$"
		}
		return currency + " "
	}
}

func normalize(input string) (string, error) {
	s := stripCurrency(strings.TrimSpace(input))
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return "", errors.New("enter an amount")
	}
	s = strings.TrimPrefix(s, "+")
	if s == "" || strings.HasPrefix(s, "-") {
		return "", errors.New("enter an amount greater than zero")
	}
	hasComma := strings.Contains(s, ",")
	hasDot := strings.Contains(s, ".")
	switch {
	case hasComma && hasDot:
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") {
			s = strings.ReplaceAll(s, ".", "")
			s = strings.ReplaceAll(s, ",", ".")
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case hasComma:
		s = strings.ReplaceAll(s, ",", ".")
	}
	return s, nil
}

func parseNormalized(s string) (int64, error) {
	if strings.Count(s, ".") > 1 {
		return 0, errors.New("enter an amount like 123.45")
	}
	whole, fracPart, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if !allDigits(whole) || (hasFrac && !allDigits(fracPart)) {
		return 0, errors.New("enter an amount like 123.45")
	}
	if hasFrac && (fracPart == "" || len(fracPart) > 2) {
		return 0, errors.New("use at most two decimal places")
	}
	if len(fracPart) == 1 {
		fracPart += "0"
	}
	if fracPart == "" {
		fracPart = "00"
	}
	units, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, errors.New("amount is too large")
	}
	frac, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return 0, errors.New("enter an amount like 123.45")
	}
	if units > maxUnits || (units == maxUnits && frac > 0) {
		return 0, errors.New("amount is too large")
	}
	return units*100 + frac, nil
}

func stripCurrency(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"CA$", "A$", "R$", "€", "£", "$"} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(s, prefix))
		}
	}
	return s
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
