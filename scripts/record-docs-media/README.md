# Documentation Media Recorder

Records videos of Sharko's UI flows for converting to GIFs used in the README and documentation.

## Prerequisites

1. Sharko demo server running:
   ```bash
   make demo
   ```

2. Install dependencies:
   ```bash
   cd scripts/record-docs-media
   npm install
   npm run install-browser
   ```

## Recording

Run the script:

```bash
node record-both-flows.mjs
```

The script will:
- Open a browser window at 1280x800
- Log in with demo credentials (admin/admin)
- Record two flows:
  1. Adding kyverno from the Marketplace (marketplace → search → detail → preview → Add to catalog → PR)
  2. Enabling datadog on cluster dev-us (cluster detail → Add addon → select → preview → confirm → PR)

Videos are saved to `../../_video-recordings-final/`.

## Converting to GIFs

### Using gifski (recommended, best quality)

```bash
# Install gifski (macOS)
brew install gifski

# Convert video to GIF (~10-12 fps, good balance of size and smoothness)
gifski -o add-addon.gif --fps 10 --quality 90 _video-recordings/video-1.webm
gifski -o enable-on-cluster.gif --fps 10 --quality 90 _video-recordings/video-2.webm
```

Target size: under 5 MB per GIF.

### Using ffmpeg (fallback)

```bash
# Generate palette for better colors
ffmpeg -i _video-recordings/video-1.webm -vf "fps=10,scale=1280:-1:flags=lanczos,palettegen" palette.png

# Create GIF using the palette
ffmpeg -i _video-recordings/video-1.webm -i palette.png -filter_complex "fps=10,scale=1280:-1:flags=lanczos[x];[x][1:v]paletteuse" add-addon.gif

# Repeat for other videos
```

## Pacing Rules

The script follows the owner's video pacing rules:
- One idea per screen
- 5-6 second holds on key frames
- Slow mouse movements and typing
- No zoom effects
- Well-known addons only (cert-manager)
- No private information on screen

## Output

After recording and converting:
1. Place GIFs in `docs/site/assets/gifs/`
2. Add to README after the intro paragraph
3. Add to relevant docs pages (marketplace.md, clusters.md, dashboard.md)
