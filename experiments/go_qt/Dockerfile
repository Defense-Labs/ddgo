FROM fedora:41

RUN dnf -y --setopt=install_weak_deps=False install \
        gcc-c++ \
        golang \
        pkgconf-pkg-config \
        qt6-qtbase-devel \
        qt6-qtdeclarative-devel \
        qt6-qtquick3d-devel \
        qt6-qtshadertools-devel \
    && dnf clean all

ENV CGO_ENABLED=1
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN go build -o /opt/ddgo-go-qt-demo .

CMD ["/opt/ddgo-go-qt-demo"]
