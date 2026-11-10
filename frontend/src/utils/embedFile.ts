export function isEmbedImageFile(file: File): boolean {
  if (file.type.startsWith('image/')) return true
  const name = file.name.toLowerCase()
  return ['.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp'].some((ext) => name.endsWith(ext))
}

export function fileToDataURI(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = reject
    reader.readAsDataURL(file)
  })
}
