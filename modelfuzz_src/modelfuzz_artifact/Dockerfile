FROM ubuntu:22.04

# Install dependencies
RUN apt-get update && \
    apt-get install -y \
        build-essential cmake git openjdk-17-jdk \
        vim wget software-properties-common psmisc
        
RUN apt-get update && apt-get install -y curl

WORKDIR /Fuzzing

# Installing go
RUN wget https://go.dev/dl/go1.24.4.linux-amd64.tar.gz
RUN tar -C /usr/local -zxf go1.24.4.linux-amd64.tar.gz

ENV PATH="$PATH:/usr/local/go/bin"
RUN mkdir -p /go
ENV GOPATH="/go"
ENV PATH="$PATH:$GOPATH/bin"

# Set environment variables for Java
ENV JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64
ENV JAVA_TOOL_OPTIONS=-Dfile.encoding=UTF8
ENV PATH="${JAVA_HOME}/bin:${PATH}"

# Install .NET SDK 7.0 manually
RUN wget https://dot.net/v1/dotnet-install.sh && \
    chmod +x dotnet-install.sh && \
    ./dotnet-install.sh --version 7.0.101 --install-dir /usr/share/dotnet && \
    ln -s /usr/share/dotnet/dotnet /usr/bin/dotnet

# Set environment
ENV DOTNET_ROOT=/usr/share/dotnet
ENV PATH=$DOTNET_ROOT:$PATH

# Installing ant
RUN wget https://archive.apache.org/dist/ant/binaries/apache-ant-1.10.13-bin.tar.gz
RUN tar -zxf apache-ant-1.10.13-bin.tar.gz

ENV ANT_HOME=/Fuzzing/apache-ant-1.10.13
ENV PATH="$PATH:/Fuzzing/apache-ant-1.10.13/bin"

ENV PATH="$PATH:/Fuzzing/scripts"

# Move the directories
COPY . /Fuzzing

# Build the project
RUN setup.sh

ENTRYPOINT /bin/bash