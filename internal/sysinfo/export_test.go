package sysinfo

// Test seams for the parsers, which are the part worth checking without a
// live ps or top.
var (
	ParseTopMem = parseTopMem
	ParsePSLine = parsePSLine
)
