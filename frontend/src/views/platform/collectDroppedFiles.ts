// 拖拽文件夹时，Chrome/Edge 会用"假"目录条目（size 0、无扩展名）填充
// dataTransfer.files，而非文件夹内的真实文件。只有 webkitGetAsEntry()
// 能递归遍历拖入的目录，因此必须优先尝试它，仅在入口 API 不可用时才回退
// 到 dataTransfer.files。Firefox 在拖拽文件夹时 dataTransfer.files 为空，
// 同样需要走 entry 遍历路径。

const isHiddenSegment = (segment: string): boolean => segment.startsWith('.')

export const setRelativePath = (file: File, relativePath: string): void => {
  try {
    Object.defineProperty(file, 'webkitRelativePath', {
      value: relativePath,
      writable: false,
      enumerable: true,
      configurable: true,
    })
  } catch {
    // 旧版 Safari 可能拒绝在 File 上 defineProperty，这些文件
    // 不会携带相对路径，将以平铺方式上传。
  }
}

const readAllDirEntries = (reader: { readEntries: Function }): Promise<any[]> => {
  return new Promise((resolve) => {
    const collected: any[] = []
    const readBatch = () => {
      reader.readEntries((entries: any[]) => {
        if (!entries || entries.length === 0) {
          resolve(collected)
        } else {
          collected.push(...entries)
          readBatch()
        }
      }, () => resolve(collected))
    }
    readBatch()
  })
}

export const traverseEntry = (entry: any, path: string): Promise<File[]> => {
  return new Promise((resolve) => {
    try {
      if (!entry) {
        resolve([])
        return
      }
      if (entry.isFile) {
        if (typeof entry.file !== 'function') {
          resolve([])
          return
        }
        entry.file((file: File) => {
          // 仅对目录内的文件设置 webkitRelativePath，与
          // <input webkitdirectory> 行为一致。顶层拖入的文件
          // 保持空值，作为普通文件上传。
          if (path) {
            const relativePath = `${path}/${file.name}`
            if (relativePath.split('/').some(isHiddenSegment)) {
              resolve([])
              return
            }
            setRelativePath(file, relativePath)
          }
          resolve([file])
        }, () => resolve([]))
      } else if (entry.isDirectory) {
        const dirPath = path ? `${path}/${entry.name}` : entry.name
        // 跳过隐藏目录（.git、.DS_Store 等）
        if (dirPath.split('/').some(isHiddenSegment)) {
          resolve([])
          return
        }
        if (typeof entry.createReader !== 'function') {
          resolve([])
          return
        }
        readAllDirEntries(entry.createReader())
          .then(children => Promise.all(
            children.map(c => traverseEntry(c, dirPath).catch(() => [] as File[])),
          ))
          .then(results => resolve(results.flat()))
          .catch(() => resolve([]))
      } else {
        resolve([])
      }
    } catch {
      resolve([])
    }
  })
}

export const collectDroppedFiles = async (event: DragEvent): Promise<File[]> => {
  const dataTransfer = event.dataTransfer
  const items = dataTransfer?.items ? Array.from(dataTransfer.items) : []
  // DataTransfer 仅在 drop 同步阶段保证可用，回退列表必须先拷贝。
  const fallbackFiles = dataTransfer?.files ? Array.from(dataTransfer.files) : []

  if (items.length === 0) {
    return fallbackFiles
  }

  const fileItems = items.filter(item => item.kind === 'file')
  if (fileItems.length === 0) {
    return fallbackFiles
  }

  const pairs = fileItems.map(item => {
    try {
      return { item, entry: (item as any).webkitGetAsEntry?.() ?? null }
    } catch {
      return { item, entry: null }
    }
  })
  const usable = pairs.filter(p => p.entry != null)
  if (usable.length === 0) {
    // 浏览器不支持 webkitGetAsEntry，退回已快照的 FileList。
    return fallbackFiles
  }

  const results = await Promise.all(usable.map(async ({ item, entry }) => {
    try {
      if (entry.isDirectory) {
        return await traverseEntry(entry, '')
      }
      // 顶层文件用 getAsFile：同步、保留空 webkitRelativePath。
      const file = item.getAsFile()
      if (file) return [file]
      return await traverseEntry(entry, '')
    } catch {
      if (entry?.isDirectory) return []
      const file = item.getAsFile()
      return file ? [file] : []
    }
  }))

  // 只要拿到了 FileSystemEntry，就采信遍历结果（包括空数组）。
  // 空文件夹 / 全是隐藏文件时若再回退 dataTransfer.files，
  // Chrome/Edge 会再次交出 size 0 的幽灵目录项。
  return results.flat()
}
