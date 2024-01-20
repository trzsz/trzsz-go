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
)

type fileReader interface {
	io.ReadCloser
	getFile() *os.File
	getSize() int64
}

type fileWriter interface {
	io.WriteCloser
	getFile() *os.File
}

type simpleFileReader struct {
	file *os.File
	size int64
}

func (f *simpleFileReader) Read(p []byte) (int, error) {
	return f.file.Read(p)
}

func (f *simpleFileReader) Close() error {
	return f.file.Close()
}

func (f *simpleFileReader) getFile() *os.File {
	return f.file
}

func (f *simpleFileReader) getSize() int64 {
	return f.size
}

type simpleFileWriter struct {
	file *os.File
}

func (f *simpleFileWriter) Write(p []byte) (int, error) {
	return f.file.Write(p)
}

func (f *simpleFileWriter) Close() error {
	return f.file.Close()
}

func (f *simpleFileWriter) getFile() *os.File {
	return f.file
}

func (t *trzszTransfer) archiveSourceFiles(sourceFiles []*sourceFile) []*sourceFile {
	if t.transferConfig.Overwrite || t.transferConfig.Protocol < kProtocolVersion4 || len(sourceFiles) == 0 {
		return sourceFiles
	}
	newSrcFiles := make([]*sourceFile, sourceFiles[len(sourceFiles)-1].PathID+1)
	for _, srcFile := range sourceFiles {
		if newSrcFiles[srcFile.PathID] == nil {
			newSrcFiles[srcFile.PathID] = srcFile
		} else {
			newSrcFiles[srcFile.PathID].SubFiles = append(newSrcFiles[srcFile.PathID].SubFiles, srcFile)
		}
	}
	return newSrcFiles
}

type archiveFileReader struct {
	files []*sourceFile
	src   *sourceFile
	idx   int
	buf   []byte
	file  *os.File
	left  int64
	size  int64
}

func (f *archiveFileReader) Read(p []byte) (int, error) {
	for {
		if f.src == nil {
			if f.idx >= len(f.files) {
				return 0, io.EOF
			}
			f.src = f.files[f.idx]
			f.idx++
			f.buf = append([]byte(f.src.Header), '\n')
			if f.file != nil {
				f.file.Close()
			}
			if f.src.IsDir {
				f.file = nil
			} else {
				var err error
				f.file, err = os.Open(f.src.AbsPath)
				if err != nil {
					return 0, simpleTrzszError("Open [%s] error: %v", f.src.AbsPath, err)
				}
			}
			f.left = f.src.Size
		}
		if len(f.buf) > 0 {
			n := copy(p, f.buf)
			f.buf = f.buf[n:]
			return n, nil
		}
		if f.file != nil {
			m := minInt64(int64(len(p)), f.left)
			n, err := f.file.Read(p[:int(m)])
			f.left -= int64(n)
			if err == io.EOF {
				if f.left != 0 {
					return 0, simpleTrzszError("EOF but left [%d] <> 0", f.left)
				}
			} else if err != nil {
				return 0, simpleTrzszError("Read [%s] error: %v", f.src.AbsPath, err)
			}
			if f.left == 0 {
				f.src = nil
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		f.src = nil
	}
}

func (f *archiveFileReader) Close() error {
	if f.file != nil {
		return f.file.Close()
	}
	return nil
}

func (f *archiveFileReader) getFile() *os.File {
	return nil
}

func (f *archiveFileReader) getSize() int64 {
	return f.size
}

func (t *trzszTransfer) newArchiveReader(srcFile *sourceFile) (fileReader, error) {
	size := int64(0)
	for _, file := range srcFile.SubFiles {
		source, err := file.marshalSourceFile()
		if err != nil {
			return nil, err
		}
		file.Header = encodeString(source)
		size += int64(len(file.Header)) + 1
		if !file.IsDir {
			size += file.Size
		}
	}
	return &archiveFileReader{files: srcFile.SubFiles, size: size}, nil
}

type archiveFileWriter struct {
	transfer *trzszTransfer
	path     string
	buf      []byte
	file     fileWriter
	left     int64
}

func (f *archiveFileWriter) Write(p []byte) (int, error) {
	if f.left > 0 && f.file != nil {
		m := minInt64(f.left, int64(len(p)))
		n, err := f.file.Write(p[:int(m)])
		f.left -= int64(n)
		return n, err
	}
	idx := bytes.IndexByte(p, '\n')
	if idx < 0 {
		f.buf = append(f.buf, p...)
		return len(p), nil
	}
	hdr := p[:idx]
	if f.buf != nil {
		hdr = append(f.buf, hdr...)
		f.buf = nil
	}
	jsonName, err := decodeString(string(hdr))
	if err != nil {
		return 0, simpleTrzszError("Decode archive header error: %v", err)
	}
	srcFile, err := unmarshalSourceFile(string(jsonName))
	if err != nil {
		return 0, err
	}
	file, _, err := f.transfer.createDirOrFile(f.path, srcFile, true)
	if err != nil {
		return 0, err
	}
	f.file = file
	f.left = srcFile.Size
	return idx + 1, nil
}

func (f *archiveFileWriter) Close() error {
	if f.file != nil {
		return f.file.Close()
	}
	return nil
}

func (f *archiveFileWriter) getFile() *os.File {
	return nil
}

func (t *trzszTransfer) newArchiveWriter(destPath string, srcFile *sourceFile, fullPath string) (fileWriter, error) {
	if !srcFile.IsDir {
		return nil, simpleTrzszError("Archive is not a directory: %s", srcFile.getFileName())
	}
	if err := t.doCreateDirectory(fullPath, srcFile.Perm); err != nil {
		return nil, err
	}
	return &archiveFileWriter{transfer: t, path: destPath}, nil
}
