package git

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDiff(t *testing.T) {
	input := `diff --git a/foo.go b/foo.go
index 1111111..2222222 100644
--- a/foo.go
+++ b/foo.go
@@ -10,3 +10,4 @@
 line10
-line11
+line11-changed
+line11b
 line12
diff --git a/bar.go b/bar.go
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/bar.go
@@ -0,0 +1,2 @@
+package bar
+
`
	patches, err := ParseUnifiedDiff([]byte(input))
	require.NoError(t, err)
	require.Len(t, patches, 2)

	require.Equal(t, "foo.go", patches[0].Path)
	require.Len(t, patches[0].Hunks, 1)
	h0 := patches[0].Hunks[0]
	require.Equal(t, int32(10), h0.NewStart)
	require.Equal(t, int32(4), h0.NewLines)
	// 4 lines emitted into the new file at lines 10..13
	wantLines := []DiffLine{
		{NewLine: 10, Op: ' ', Text: "line10"},
		{NewLine: 11, Op: '+', Text: "line11-changed"},
		{NewLine: 12, Op: '+', Text: "line11b"},
		{NewLine: 13, Op: ' ', Text: "line12"},
	}
	require.Equal(t, wantLines, h0.Lines)

	require.Equal(t, "bar.go", patches[1].Path)
	require.True(t, patches[1].NewFile)
}
