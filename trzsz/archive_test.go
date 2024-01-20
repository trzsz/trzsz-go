/*
MIT License

Copyright (c) 2022-2024 The Trzsz Authors.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package trzsz

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertFileEqual(t *testing.T, path1, path2 string) {
	t.Helper()
	file1, err := os.ReadFile(path1)
	require.Nil(t, err)
	file2, err := os.ReadFile(path2)
	require.Nil(t, err)
	assert.Equal(t, file1, file2)
}

func assertDirEqual(t *testing.T, path1, path2 string) {
	t.Helper()
	info1, err := os.Stat(path1)
	require.Nil(t, err)
	assert.True(t, info1.IsDir())
	info2, err := os.Stat(path2)
	require.Nil(t, err)
	assert.True(t, info2.IsDir())
	dir1, err := os.Open(path1)
	require.Nil(t, err)
	dir2, err := os.Open(path2)
	require.Nil(t, err)
	files1, err := dir1.Readdir(-1)
	require.Nil(t, err)
	files2, err := dir2.Readdir(-1)
	require.Nil(t, err)
	require.Equal(t, len(files1), len(files2))
	for _, file1 := range files1 {
		p1 := filepath.Join(path1, file1.Name())
		info1, err := os.Stat(p1)
		require.Nil(t, err)
		p2 := filepath.Join(path2, file1.Name())
		info2, err := os.Stat(p2)
		require.Nil(t, err)
		assert.Equal(t, info1.IsDir(), info2.IsDir())
		if info1.IsDir() {
			assertDirEqual(t, p1, p2)
		} else {
			assertFileEqual(t, p1, p2)
		}
	}
}

func TestArchiveReadAndWrite(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	testPath, err := os.MkdirTemp("", "trzsz_test_")
	require.Nil(err)
	defer os.RemoveAll(testPath)

	createFile := func(paths []string, content string) string {
		t.Helper()
		absPath := filepath.Join(append([]string{testPath}, paths...)...)
		file, err := os.Create(absPath)
		require.Nil(err)
		defer file.Close()
		_, err = file.WriteString(content)
		require.Nil(err)
		return absPath
	}
	createDir := func(paths []string) string {
		t.Helper()
		absPath := filepath.Join(append([]string{testPath}, paths...)...)
		err := os.MkdirAll(absPath, 0755)
		require.Nil(err)
		return absPath
	}

	emptyDir := createDir([]string{"empty_dir"})
	subFolder := createDir([]string{"sub_folder"})
	emptyFile := createFile([]string{"empty_file"}, "")
	createDir([]string{"sub_folder", "中文"})
	file1 := createFile([]string{"sub_folder", "file1"}, strings.Repeat("file content in file1.\n", 100))
	createFile([]string{"sub_folder", "中文", "文件2"}, strings.Repeat("file content in 文件2.\n", 100))
	createFile([]string{"sub_folder", "中文", "file3"}, strings.Repeat("file content in file3.\n", 100))

	srcFiles, err := checkPathsReadable([]string{emptyDir, subFolder, emptyFile}, true)
	require.Nil(err)
	assert.Equal(7, len(srcFiles))

	transfer := newTransfer(nil, nil, false, nil)
	transfer.transferConfig.Overwrite = false
	transfer.transferConfig.Protocol = kProtocolVersion4
	defer transfer.deleteCreatedFiles()

	archiveFiles := transfer.archiveSourceFiles(srcFiles)
	require.Equal(3, len(archiveFiles))
	assert.Equal(0, len(archiveFiles[0].SubFiles))
	assert.Equal(4, len(archiveFiles[1].SubFiles))
	assert.Equal(0, len(archiveFiles[2].SubFiles))
	for _, f := range archiveFiles[1].SubFiles {
		assert.Equal(archiveFiles[1].PathID, f.PathID)
	}

	srcFiles, err = checkPathsReadable([]string{testPath}, true)
	require.Nil(err)
	assert.Equal(8, len(srcFiles))
	archiveFiles = transfer.archiveSourceFiles(srcFiles)
	require.Equal(1, len(archiveFiles))
	assert.Equal(7, len(archiveFiles[0].SubFiles))
	for _, f := range archiveFiles[0].SubFiles {
		assert.Equal(archiveFiles[0].PathID, f.PathID)
	}

	f1, err := os.OpenFile(file1, os.O_APPEND|os.O_WRONLY, 0)
	require.Nil(err)
	s1, err := f1.Stat()
	require.Nil(err)
	size1 := s1.Size()
	_, err = f1.WriteString("append content")
	require.Nil(err)
	require.Nil(f1.Close())

	var savePath []string
	for i, N := range []int{1, 2, 3, 10, 135, 136, 137, 171, 172, 173, 200, 32 * 1024} {
		srcFile := archiveFiles[0]
		srcFile.PathID = i
		for _, f := range srcFile.SubFiles {
			f.PathID = i
		}
		reader, err := transfer.newArchiveReader(srcFile)
		require.Nil(err)
		source, err := archiveFiles[0].marshalSourceFile()
		require.Nil(err)
		srcFile, err = unmarshalSourceFile(source)
		require.Nil(err)
		writer, name, err := transfer.createDirOrFile(os.TempDir(), srcFile, true)
		require.Nil(err)
		var buffer bytes.Buffer
		for {
			buf := make([]byte, N)
			n, err := reader.Read(buf)
			if err == io.EOF {
				break
			}
			require.Nil(err)
			if n > 0 {
				buffer.Write(buf[:n])
			}
		}
		n := 0
		for n < buffer.Len() {
			m := minInt(buffer.Len()-n, N)
			buf := buffer.Bytes()[n : m+n]
			require.Nil(writeAll(writer, buf))
			n += m
		}
		reader.Close()
		writer.Close()
		savePath = append(savePath, filepath.Join(os.TempDir(), name))
	}

	f1, err = os.OpenFile(file1, os.O_RDWR, 0)
	require.Nil(err)
	require.Nil(f1.Truncate(size1))
	require.Nil(f1.Close())
	for _, path := range savePath {
		assertDirEqual(t, testPath, path)
	}
}
