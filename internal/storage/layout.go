package storage

import (
	"path"
	"strings"
)

func volumeRel(class, id, format string) string {
	switch class {
	case ClassVMDisk:
		return path.Join("volumes", ClassVMDisk, id+"."+extFor(format))
	case ClassTemplate:
		return path.Join("volumes", ClassTemplate, id+"."+extFor(format))
	case ClassContainerRoot:
		return path.Join("volumes", ClassContainerRoot, id)
	case ClassBackupStaging:
		return path.Join("volumes", ClassBackupStaging, id)
	default:
		return path.Join("volumes", class, id)
	}
}

func libraryRel(kind, id, display string) string {
	ext := libraryExt(kind, display)
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return path.Join("library", kind, id+ext)
}

func tmpRel(id string) string {
	return path.Join("tmp", "upload-"+id)
}

func extFor(format string) string {
	switch format {
	case FormatRaw:
		return "raw"
	default:
		return "qcow2"
	}
}

func libraryExt(kind, display string) string {
	display = strings.ToLower(path.Ext(display))
	switch kind {
	case LibraryISO:
		if display == ".iso" || display == ".img" {
			return strings.TrimPrefix(display, ".")
		}
		return "iso"
	case LibraryCloudImage, LibraryDiskImage:
		switch display {
		case ".qcow2", ".img", ".raw", ".qcow":
			return strings.TrimPrefix(display, ".")
		default:
			return "qcow2"
		}
	default:
		return "bin"
	}
}

func classFromRel(rel string) (class, kind, format string) {
	rel = strings.TrimPrefix(rel, "/")
	switch {
	case strings.HasPrefix(rel, "volumes/"+ClassVMDisk+"/"):
		return ClassVMDisk, KindBlock, formatFromName(rel)
	case strings.HasPrefix(rel, "volumes/"+ClassTemplate+"/"):
		return ClassTemplate, KindBlock, formatFromName(rel)
	case strings.HasPrefix(rel, "volumes/"+ClassContainerRoot+"/"):
		return ClassContainerRoot, KindFilesystem, FormatDirectory
	case strings.HasPrefix(rel, "volumes/"+ClassBackupStaging+"/"):
		return ClassBackupStaging, KindFilesystem, FormatDirectory
	default:
		return "", "", ""
	}
}

func formatFromName(rel string) string {
	if strings.HasSuffix(rel, ".raw") {
		return FormatRaw
	}
	return FormatQCOW2
}

func uuidFromRel(rel string) string {
	base := path.Base(rel)
	if i := strings.IndexByte(base, '.'); i > 0 {
		return base[:i]
	}
	return base
}

func poolDirs() []string {
	return []string{
		"volumes/" + ClassVMDisk,
		"volumes/" + ClassContainerRoot,
		"volumes/" + ClassTemplate,
		"volumes/" + ClassBackupStaging,
		"library/" + LibraryISO,
		"library/" + LibraryCloudImage,
		"library/" + LibraryDiskImage,
		"tmp",
	}
}
