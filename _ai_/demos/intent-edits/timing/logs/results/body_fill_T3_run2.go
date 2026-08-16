package models

// SumNoteFileSizes returns the total SizeBytes across files.
func SumNoteFileSizes(files []NoteFile) int64 {
var total int64  
for _, file := range files {  
    total += file.SizeBytes  
}  
return total
}
