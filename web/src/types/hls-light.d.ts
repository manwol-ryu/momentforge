// hls.js ships its light build (no DRM, subtitles or alternate audio) without
// separate typings; its API is the same as the full build.
declare module "hls.js/light" {
  import Hls from "hls.js";
  export default Hls;
}
