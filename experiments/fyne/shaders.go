package main

// These sources intentionally draw only a fixed procedural scene. They prove
// that canvas.Shader is a rectangle fragment-shader API; they do not receive
// the generated Segment slice and are not a toolpath rendering implementation.
const viewportShaderDesktop = `#version 110
uniform vec2 frame;
uniform vec4 bounds;
uniform float yaw;
uniform float pitch;
uniform float zoom;
uniform float time;

mat3 rx(float a) { float c=cos(a), s=sin(a); return mat3(1,0,0, 0,c,s, 0,-s,c); }
mat3 ry(float a) { float c=cos(a), s=sin(a); return mat3(c,0,-s, 0,1,0, s,0,c); }
float box(vec3 p, vec3 b) { vec3 d=abs(p)-b; return length(max(d,0.0))+min(max(d.x,max(d.y,d.z)),0.0); }
float scene(vec3 p) { return box(ry(yaw)*rx(pitch)*p, vec3(0.58)); }

void main() {
    vec2 extent=vec2(bounds[2]-bounds[0],bounds[3]-bounds[1]);
    vec2 center=vec2((bounds[0]+bounds[2])*0.5,frame.y-(bounds[1]+bounds[3])*0.5);
    vec2 uv=(gl_FragCoord.xy-center)/(0.5*min(extent.x,extent.y));
    vec3 ro=vec3(0,0,-3.0/max(zoom,0.15));
    vec3 rd=normalize(vec3(uv,1.7));
    float t=0.0; bool hit=false; vec3 p=ro;
    for(int i=0;i<32;i++) { p=ro+rd*t; float d=scene(p); if(d<0.002){hit=true;break;} t+=d; if(t>8.0)break; }
    vec3 bg=vec3(0.035,0.055,0.075)+0.04*vec3(uv.y+1.0);
    if(!hit) { gl_FragColor=vec4(bg,1); return; }
    float glow=0.72+0.12*sin(time*1.4);
    gl_FragColor=vec4(vec3(0.08,0.48,0.70)*glow,1);
}`

const viewportShaderES = `#version 100
#ifdef GL_ES
precision mediump float;
precision mediump int;
#endif
uniform vec2 frame;
uniform vec4 bounds;
uniform float yaw;
uniform float pitch;
uniform float zoom;
uniform float time;

mat3 rx(float a) { float c=cos(a), s=sin(a); return mat3(1,0,0, 0,c,s, 0,-s,c); }
mat3 ry(float a) { float c=cos(a), s=sin(a); return mat3(c,0,-s, 0,1,0, s,0,c); }
float box(vec3 p, vec3 b) { vec3 d=abs(p)-b; return length(max(d,0.0))+min(max(d.x,max(d.y,d.z)),0.0); }
float scene(vec3 p) { return box(ry(yaw)*rx(pitch)*p, vec3(0.58)); }

void main() {
    vec2 extent=vec2(bounds[2]-bounds[0],bounds[3]-bounds[1]);
    vec2 center=vec2((bounds[0]+bounds[2])*0.5,frame.y-(bounds[1]+bounds[3])*0.5);
    vec2 uv=(gl_FragCoord.xy-center)/(0.5*min(extent.x,extent.y));
    vec3 ro=vec3(0,0,-3.0/max(zoom,0.15));
    vec3 rd=normalize(vec3(uv,1.7));
    float t=0.0; bool hit=false; vec3 p=ro;
    for(int i=0;i<32;i++) { p=ro+rd*t; float d=scene(p); if(d<0.002){hit=true;break;} t+=d; if(t>8.0)break; }
    vec3 bg=vec3(0.035,0.055,0.075)+0.04*vec3(uv.y+1.0);
    if(!hit) { gl_FragColor=vec4(bg,1); return; }
    float glow=0.72+0.12*sin(time*1.4);
    gl_FragColor=vec4(vec3(0.08,0.48,0.70)*glow,1);
}`
