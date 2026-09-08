# resources/assets

Source art for the app icon. Nothing here is loaded at runtime; `scripts/build.sh`
turns it into `GoTab.app/Contents/Resources/AppIcon.icns` at build time.

| file | what | edit? |
|---|---|---|
| `ico.png` | the original artwork, as delivered — 496×664, portrait, transparent | this is the human source; replace it to change the icon |
| `icon-1024.png` | `ico.png` centred on a 1024×1024 transparent square, art at 92% of the tile — the master `build.sh` actually consumes | regenerate from `ico.png`, don't hand-edit |

## Regenerating `icon-1024.png` after changing `ico.png`

The square master is produced once and committed so a build needs only `sips` +
`iconutil` (both base-system), not a Swift toolchain. To rebuild it:

```sh
swift - <<'SWIFT' resources/assets/ico.png resources/assets/icon-1024.png
import AppKit, CoreGraphics
let src = CommandLine.arguments[1], dst = CommandLine.arguments[2]
let canvas = 1024, margin: CGFloat = 0.92
let rep = NSBitmapImageRep(data: NSImage(contentsOfFile: src)!.tiffRepresentation!)!
let sw = CGFloat(rep.pixelsWide), sh = CGFloat(rep.pixelsHigh)
let ctx = CGContext(data: nil, width: canvas, height: canvas, bitsPerComponent: 8,
                    bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(),
                    bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
ctx.clear(CGRect(x: 0, y: 0, width: canvas, height: canvas))
ctx.interpolationQuality = .high
let s = (CGFloat(canvas) * margin) / max(sw, sh), dw = sw * s, dh = sh * s
ctx.draw(rep.cgImage!, in: CGRect(x: (CGFloat(canvas) - dw) / 2, y: (CGFloat(canvas) - dh) / 2, width: dw, height: dh))
try! NSBitmapImageRep(cgImage: ctx.makeImage()!).representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: dst))
SWIFT
```

Then `./scripts/build.sh` regenerates `AppIcon.icns` from it. If the icon looks
weak at 16/32 px that is the source art's detail density, not the pipeline —
those sizes want a simplified glyph, which would be a separate hand-drawn asset.
