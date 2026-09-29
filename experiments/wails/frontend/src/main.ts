import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { GetToolpath } from '../wailsjs/go/main/App';
import './style.css';

// Wails serialises the Go Point/Segment structures using their json tags.
interface Point { x: number; y: number; z: number; }
interface Segment { start: Point; end: Point; rapid: boolean; }

const app = document.querySelector<HTMLDivElement>('#app');
if (!app) throw new Error('Missing application root');
app.innerHTML = `
  <main>
    <div id="viewport" aria-label="Interactive 3D toolpath viewport"></div>
    <footer>
      <button id="reset-view" type="button">Reset View</button>
      <button id="animate" type="button">Animate</button>
      <span id="status">Loading Go-generated toolpath…</span>
    </footer>
  </main>`;

const viewport = document.querySelector<HTMLDivElement>('#viewport');
const resetButton = document.querySelector<HTMLButtonElement>('#reset-view');
const animateButton = document.querySelector<HTMLButtonElement>('#animate');
const status = document.querySelector<HTMLSpanElement>('#status');
if (!viewport || !resetButton || !animateButton || !status) throw new Error('Missing UI element');

const scene = new THREE.Scene();
scene.background = new THREE.Color(0x151b24);
const camera = new THREE.PerspectiveCamera(45, 1, 0.1, 1000);
const initialCamera = new THREE.Vector3(135, 125, 145);
const initialTarget = new THREE.Vector3(0, 0, 0);
camera.position.copy(initialCamera);

let renderer: THREE.WebGLRenderer;
try {
    renderer = new THREE.WebGLRenderer({ antialias: true });
} catch (error) {
    status!.textContent = `WebGL renderer creation failed: ${String(error)}`;
    throw error;
}
renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
viewport.append(renderer.domElement);

const controls = new OrbitControls(camera, renderer.domElement);
controls.target.copy(initialTarget);
controls.enableDamping = true;
controls.dampingFactor = 0.08;
controls.update();

// CNC X/Y/Z maps to Three X/Z/Y respectively: machine Z is visually vertical.
// Negating machine Y keeps the visual coordinate system right-handed.
function toThree(point: Point, target: THREE.Vector3): THREE.Vector3 {
    return target.set(point.x, point.z, -point.y);
}

scene.add(new THREE.AxesHelper(32));
scene.add(new THREE.GridHelper(140, 28, 0x516274, 0x2c3946));
const marker = new THREE.Mesh(
    new THREE.SphereGeometry(2.2, 16, 12),
    new THREE.MeshBasicMaterial({ color: 0xffe07a }),
);
scene.add(marker);

const resizeObserver = new ResizeObserver(() => {
    const { width, height } = viewport.getBoundingClientRect();
    if (width === 0 || height === 0) return;
    camera.aspect = width / height;
    camera.updateProjectionMatrix();
    renderer.setSize(width, height, false);
});
resizeObserver.observe(viewport);

let segments: Segment[] = [];
let playing = false;
let playbackPosition = 0;
let lastFrame = performance.now();
const markerPosition = new THREE.Vector3();

function addLineGeometry(path: Segment[], colour: number): void {
    const positions = new Float32Array(path.length * 6);
    const converted = new THREE.Vector3();
    let offset = 0;
    for (const segment of path) {
        toThree(segment.start, converted);
        positions[offset++] = converted.x;
        positions[offset++] = converted.y;
        positions[offset++] = converted.z;
        toThree(segment.end, converted);
        positions[offset++] = converted.x;
        positions[offset++] = converted.y;
        positions[offset++] = converted.z;
    }
    const geometry = new THREE.BufferGeometry();
    geometry.setAttribute('position', new THREE.BufferAttribute(positions, 3));
    scene.add(new THREE.LineSegments(geometry, new THREE.LineBasicMaterial({ color: colour })));
}

function setMarker(position: number): void {
    if (segments.length === 0) return;
    const segmentIndex = Math.min(Math.floor(position), segments.length - 1);
    const fraction = Math.min(position - segmentIndex, 1);
    const segment = segments[segmentIndex];
    markerPosition.set(
        segment.start.x + (segment.end.x - segment.start.x) * fraction,
        segment.start.y + (segment.end.y - segment.start.y) * fraction,
        segment.start.z + (segment.end.z - segment.start.z) * fraction,
    );
    marker.position.copy(toThree(markerPosition, marker.position));
    status!.textContent = `Segments: ${segments.length}   Current: ${segmentIndex + 1}`;
}

function resetView(): void {
    camera.position.copy(initialCamera);
    controls.target.copy(initialTarget);
    controls.update();
}

resetButton.addEventListener('click', resetView);
animateButton.addEventListener('click', () => {
    if (segments.length === 0) return;
    if (!playing && playbackPosition >= segments.length - 1) playbackPosition = 0;
    playing = !playing;
    animateButton.textContent = playing ? 'Stop' : 'Animate';
});

function render(now: number): void {
    const elapsedSeconds = Math.min((now - lastFrame) / 1000, 0.1);
    lastFrame = now;
    if (playing) {
        playbackPosition += elapsedSeconds * 180;
        if (playbackPosition >= segments.length - 1) {
            playbackPosition = segments.length - 1;
            playing = false;
            animateButton!.textContent = 'Animate';
        }
        setMarker(playbackPosition);
    }
    controls.update();
    renderer.render(scene, camera);
    requestAnimationFrame(render);
}

async function initialise(): Promise<void> {
    try {
        // This is the sole path request: generated bindings invoke App.GetToolpath in Go.
        segments = await GetToolpath() as Segment[];
        const cutting = segments.filter((segment) => !segment.rapid);
        const rapids = segments.filter((segment) => segment.rapid);
        addLineGeometry(cutting, 0x4cc9f0);
        addLineGeometry(rapids, 0xff8c42);
        setMarker(0);
    } catch (error) {
        status!.textContent = `Could not retrieve toolpath through Wails: ${String(error)}`;
        console.error(error);
    }
    requestAnimationFrame(render);
}

void initialise();
