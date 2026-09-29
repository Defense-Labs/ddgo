import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import QtQuick.Window
import QtQuick3D
import QtQuick3D.Helpers

ApplicationWindow {
    id: root
    width: 1100
    height: 760
    minimumWidth: 720
    minimumHeight: 520
    visible: true
    title: "DDGO Go + Qt 6 3D Capability Spike"
    color: "#171b22"

    property bool animating: false
    property real traversal: 0
    property int currentSegment: 0

    function resetView() {
        cameraOrigin.position = Qt.vector3d(0, -8, 0)
        cameraOrigin.eulerRotation = Qt.vector3d(-28, -38, 0)
        camera.z = 310
    }

    function updateMarker() {
        if (scene.pathSegments.length === 0)
            return

        const bounded = Math.max(0, Math.min(traversal, scene.pathSegments.length - 0.0001))
        currentSegment = Math.floor(bounded)
        const localProgress = bounded - currentSegment
        const segment = scene.pathSegments[currentSegment]
        const start = scene.toScene(segment.start)
        const end = scene.toScene(segment.end)
        scene.markerPosition = Qt.vector3d(
                    start.x + (end.x - start.x) * localProgress,
                    start.y + (end.y - start.y) * localProgress,
                    start.z + (end.z - start.z) * localProgress)
    }

    Component.onCompleted: {
        scene.loadToolpath()
        resetView()
        updateMarker()
    }

    Timer {
        interval: 16
        repeat: true
        running: root.animating
        onTriggered: {
            root.traversal += 0.85
            if (root.traversal >= scene.pathSegments.length) {
                root.traversal = 0
                root.animating = false
            }
            root.updateMarker()
        }
    }

    ColumnLayout {
        anchors.fill: parent
        spacing: 0

        View3D {
            id: viewport
            Layout.fillWidth: true
            Layout.fillHeight: true
            camera: camera

            environment: SceneEnvironment {
                clearColor: "#171b22"
                backgroundMode: SceneEnvironment.Color
                antialiasingMode: SceneEnvironment.MSAA
                antialiasingQuality: SceneEnvironment.High
            }

            Node {
                id: scene
                property var pathSegments: []
                property var cutPositions: []
                property var rapidPositions: []
                property vector3d markerPosition: Qt.vector3d(0, 0, 0)

                function toScene(point) {
                    // Quick 3D uses Y as up. CNC X/Y map to scene X/-Z and CNC
                    // Z maps to scene Y with extra scale to emphasize depth.
                    return Qt.vector3d(point.x, point.z * 2.4, -point.y)
                }

                function loadToolpath() {
                    const payload = JSON.parse(toolpathJSON)
                    const cutting = []
                    const rapids = []
                    pathSegments = payload.segments
                    for (let i = 0; i < pathSegments.length; ++i) {
                        const segment = pathSegments[i]
                        const destination = segment.rapid ? rapids : cutting
                        destination.push(toScene(segment.start))
                        destination.push(toScene(segment.end))
                    }
                    cutPositions = cutting
                    rapidPositions = rapids
                }

                Model {
                    geometry: ProceduralMesh {
                        primitiveMode: ProceduralMesh.Lines
                        positions: scene.cutPositions
                    }
                    materials: DefaultMaterial {
                        lighting: DefaultMaterial.NoLighting
                        diffuseColor: "#54d68b"
                    }
                }

                Model {
                    geometry: ProceduralMesh {
                        primitiveMode: ProceduralMesh.Lines
                        positions: scene.rapidPositions
                    }
                    materials: DefaultMaterial {
                        lighting: DefaultMaterial.NoLighting
                        diffuseColor: "#f3a34a"
                        opacity: 0.8
                    }
                }

                Model {
                    y: 0
                    eulerRotation.x: 90
                    scale: Qt.vector3d(100, 100, 100)
                    geometry: GridGeometry {
                        horizontalLines: 21
                        verticalLines: 21
                        horizontalStep: 0.1
                        verticalStep: 0.1
                    }
                    materials: DefaultMaterial {
                        lighting: DefaultMaterial.NoLighting
                        diffuseColor: "#46505f"
                        opacity: 0.45
                    }
                }

                Model {
                    geometry: ProceduralMesh {
                        primitiveMode: ProceduralMesh.Lines
                        positions: [Qt.vector3d(0, 0, 0), Qt.vector3d(115, 0, 0)]
                    }
                    materials: DefaultMaterial {
                        lighting: DefaultMaterial.NoLighting
                        diffuseColor: "#ef5350"
                    }
                }
                Model {
                    geometry: ProceduralMesh {
                        primitiveMode: ProceduralMesh.Lines
                        positions: [Qt.vector3d(0, 0, 0), Qt.vector3d(0, 0, -115)]
                    }
                    materials: DefaultMaterial {
                        lighting: DefaultMaterial.NoLighting
                        diffuseColor: "#42c96b"
                    }
                }
                Model {
                    geometry: ProceduralMesh {
                        primitiveMode: ProceduralMesh.Lines
                        positions: [Qt.vector3d(0, 0, 0), Qt.vector3d(0, 75, 0)]
                    }
                    materials: DefaultMaterial {
                        lighting: DefaultMaterial.NoLighting
                        diffuseColor: "#4b8ff7"
                    }
                }

                Model {
                    position: scene.markerPosition
                    source: "#Sphere"
                    scale: Qt.vector3d(0.065, 0.065, 0.065)
                    materials: PrincipledMaterial {
                        baseColor: "#fff176"
                        roughness: 0.35
                        metalness: 0.1
                    }
                }

                DirectionalLight {
                    eulerRotation: Qt.vector3d(-45, -35, 0)
                    brightness: 1.2
                    ambientColor: "#707780"
                }
            }

            Node {
                id: cameraOrigin
                PerspectiveCamera {
                    id: camera
                    z: 310
                    clipNear: 1
                    clipFar: 2000
                }
            }

            OrbitCameraController {
                anchors.fill: parent
                origin: cameraOrigin
                camera: camera
                panEnabled: true
            }
        }

        Rectangle {
            Layout.fillWidth: true
            Layout.preferredHeight: 66
            color: "#242a33"
            border.color: "#343c48"

            RowLayout {
                anchors.fill: parent
                anchors.leftMargin: 16
                anchors.rightMargin: 16
                spacing: 12

                Button {
                    text: "Reset View"
                    onClicked: root.resetView()
                }

                Button {
                    text: root.animating ? "Stop" : "Animate"
                    onClicked: root.animating = !root.animating
                }

                Label {
                    text: "Cutting"
                    color: "#54d68b"
                }

                Label {
                    text: "Rapid"
                    color: "#f3a34a"
                }

                Item { Layout.fillWidth: true }

                Label {
                    text: "Segment " + Math.min(root.currentSegment + 1, scene.pathSegments.length)
                          + " / " + scene.pathSegments.length
                    color: "#e5e9f0"
                }
            }
        }
    }
}
