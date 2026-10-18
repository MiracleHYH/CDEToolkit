FROM public.ecr.aws/docker/library/golang@sha256:167053a2bb901972bf2c1611f8f52c44d5fe7e762e5cab213708d82c421614db
COPY strace_6.1-0.1_amd64.deb libunwind8_1.6.2-3_amd64.deb /tmp/
RUN echo '1942d086a6244a1a9643489d3b0aa604ac44b88991d6c69217a63a193671bd4f  /tmp/strace_6.1-0.1_amd64.deb' | sha256sum -c - && echo '7b297868682836e4c87be349f17e4a56bc287586e3576503e84a5cb5485ce925  /tmp/libunwind8_1.6.2-3_amd64.deb' | sha256sum -c - && dpkg -i /tmp/libunwind8_1.6.2-3_amd64.deb /tmp/strace_6.1-0.1_amd64.deb && rm /tmp/*.deb
COPY Miniconda3-py312_24.1.2-0-Linux-x86_64.sh /tmp/miniconda.sh
RUN echo 'b978856ec3c826eb495b60e3fffe621f670c101150ebcbdeede4f961f22dc438  /tmp/miniconda.sh' | sha256sum -c - && bash /tmp/miniconda.sh -b -p /runtime/miniconda && rm /tmp/miniconda.sh
RUN useradd -u 1000 -U -m p0 && mkdir -p /work /evidence && chown 1000:1000 /work /evidence && tar -C /runtime -cf /opt/miniconda-seed.tar miniconda
ENV PATH=/runtime/miniconda/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV PYTHONDONTWRITEBYTECODE=1
